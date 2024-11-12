package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	_ "github.com/mattn/go-sqlite3"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/kbinani/screenshot"
)

var (
	db      *sql.DB
	started bool

	activityTicker     *time.Ticker
	activityTickerDone chan struct{}

	idleTicker     *time.Ticker
	idleTickerDone chan struct{}

	sessionID int64
)

const PREF_ACTIVITY_INTERVAL = "activityInterval"
const PREF_INACTIVITY_THRESHOLD = "inactivityThreshold"
const PREF_INACTIVITY_TIMEOUT = "inactivityTimeout"

func prepareDatabase() {
	if db != nil {
		return
	}

	println("Preparing database")

	var err error
	db, err = sql.Open("sqlite3", "./go-tracker.db")

	if err != nil {
		panic(err)
	}

	sqlStmt := `
    CREATE TABLE IF NOT EXISTS session (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      start DATETIME,
      end DATETIME
    );

    CREATE TABLE IF NOT EXISTS windows (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      title TEXT,
      timestamp DATETIME
    );

    CREATE TABLE IF NOT EXISTS inactivity (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      start DATETIME,
      end DATETIME
    );
  `

	_, err = db.Exec(sqlStmt)

	if err != nil {
		panic(err)
	}
}

func saveSessionStart() int64 {
	// Save in database
	result, err := db.Exec("INSERT INTO session (start) VALUES (?)", time.Now())

	if err != nil {
		log.Fatal(err)
		return -1
	}

	id, err := result.LastInsertId()

	if err != nil {
		log.Fatal(err)
		return -1
	}

	return id
}

func saveSessionEnd(id int64) {
	_, err := db.Exec("UPDATE session SET end = ? WHERE id = ?", time.Now(), id)

	if err != nil {
		log.Fatal(err)
	}
}

type Inputs struct {
	ActivityInterval    string `validate:"required,numeric,gte=0"`
	InactivityThreshold string `validate:"required,numeric,gte=0"`
	InactivityTimeout   string `validate:"required,numeric,gte=0"`
}

func main() {
	started = false

	myApp := app.NewWithID("com.github.xxrockyxx.gotracker")
	window := myApp.NewWindow("GoTracker")

	window.Resize(fyne.NewSize(250, 250))
	window.SetFixedSize(true)

	intervalEntry := newNumericalEntry()
	intervalEntry.SetPlaceHolder("60")
	intervalEntry.SetText(myApp.Preferences().StringWithFallback(PREF_ACTIVITY_INTERVAL, "60"))

	intervalContainer := container.NewVBox(
		widget.NewLabel("Activity Interval (seconds)"),
		intervalEntry,
	)

	inactivityThresholdEntry := newNumericalEntry()
	inactivityThresholdEntry.SetPlaceHolder("300")
	inactivityThresholdEntry.SetText(myApp.Preferences().StringWithFallback(PREF_INACTIVITY_THRESHOLD, "300"))

	inactivityThresholdContainer := container.NewVBox(
		widget.NewLabel("Inactivity Threshold (seconds)"),
		inactivityThresholdEntry,
	)

	inactivityTimeoutEntry := newNumericalEntry()
	inactivityTimeoutEntry.SetPlaceHolder("15")
	inactivityTimeoutEntry.SetText(myApp.Preferences().StringWithFallback(PREF_INACTIVITY_TIMEOUT, "15"))

	inactivityTimeoutContainer := container.NewVBox(
		widget.NewLabel("Inactivity Timeout (seconds)"),
		inactivityTimeoutEntry,
	)

	preferencesFormContainer := container.NewVBox(
		intervalContainer,
		layout.NewSpacer(),
		inactivityThresholdContainer,
		layout.NewSpacer(),
		inactivityTimeoutContainer,
	)

	var toggleBtn *widget.Button

	toggleBtn = widget.NewButton("Start", func() {
		if started {
			started = false

			// Update UI
			toggleBtn.SetText("Start")
			intervalEntry.Enable()
			inactivityThresholdEntry.Enable()
			inactivityTimeoutEntry.Enable()

			// Run side effects
			saveSessionEnd(sessionID)
			stopScheduler()
			stopWatchingForInactivity()
		} else {
			validate := validator.New(validator.WithRequiredStructEnabled())

			// Show first error message only
			if err := validate.Struct(&Inputs{
				ActivityInterval:    intervalEntry.Text,
				InactivityThreshold: inactivityThresholdEntry.Text,
				InactivityTimeout:   inactivityTimeoutEntry.Text,
			}); err != nil {
				// Show first error message only
				fmt.Printf("Value: %s\n", err.(validator.ValidationErrors)[0].Value())
				dialog.ShowError(errors.New(err.(validator.ValidationErrors)[0].Error()), window)
				return
			}

			started = true

			// Update UI
			toggleBtn.SetText("Stop")
			intervalEntry.Disable()
			inactivityThresholdEntry.Disable()
			inactivityTimeoutEntry.Disable()

			// Save activity interval and inactivity threshold on start
			myApp.Preferences().SetString(PREF_ACTIVITY_INTERVAL, intervalEntry.Text)
			myApp.Preferences().SetString(PREF_INACTIVITY_THRESHOLD, inactivityThresholdEntry.Text)
			myApp.Preferences().SetString(PREF_INACTIVITY_TIMEOUT, inactivityTimeoutEntry.Text)

			prepareDatabase()

			sessionID = saveSessionStart()

			if sessionID == -1 {
				panic("Failed to start session")
			}

			activityInterval, _ := strconv.Atoi(intervalEntry.Text)
			inactivityThreshold, _ := strconv.Atoi(inactivityThresholdEntry.Text)
			inactivityTimeout, _ := strconv.Atoi(inactivityTimeoutEntry.Text)

			startScheduler(time.Duration(activityInterval) * time.Second)

			watchForInactivity(WatchForInactivityParams{
				Threshold: time.Duration(inactivityThreshold) * time.Second,
				IsIdle: func(isIdle chan bool) {
					askStillWorking(myApp, time.Duration(inactivityTimeout)*time.Second, func(response string) {
						switch response {
						case PRESENCE_TIMEOUT:
							isIdle <- true
						case PRESENCE_YES, PRESENCE_CLOSED:
							isIdle <- false
						case PRESENCE_NO:
							toggleBtn.OnTapped()
							isIdle <- false
						default:
							println("Unhandled presence response")
						}
					})
				},
				OnActive: func() {
					startScheduler(time.Duration(activityInterval) * time.Second)
				},
			})
		}
	})

	content := container.New(
		layout.NewPaddedLayout(),
		container.NewVBox(
			preferencesFormContainer,
			container.New(
				layout.NewPaddedLayout(),
				toggleBtn,
			),
		),
	)

	window.SetContent(content)
	window.CenterOnScreen()

	window.ShowAndRun()
}

func saveScreenshots() {
	println("Saving screenshots")

	n := screenshot.NumActiveDisplays()

	for i := 0; i < n; i++ {
		bounds := screenshot.GetDisplayBounds(i)

		img, err := screenshot.CaptureRect(bounds)

		if err != nil {
			panic(err)
		}

		year, month, day := time.Now().Date()
		hour, minute, second := time.Now().Clock()

		// Store in flattened date format i.e YYYY-MM-DD
		screenshotDir := fmt.Sprintf("screenshots/%d-%02d-%02d", year, month, day)

		if err := os.MkdirAll(screenshotDir, os.ModePerm); err != nil {
			panic(err)
		}

		// Store in hh-mm-ss-<display number>.png format
		filename := fmt.Sprintf("%s/%02d-%02d-%02d-%d.png", screenshotDir, hour, minute, second, i)

		if file, err := os.Create(filename); err != nil {
			panic(err)
		} else {
			defer file.Close()
			png.Encode(file, img)
		}
	}
}

func saveActiveWindows() {
	println("Saving active windows")

	cmd := exec.Command("xdotool", "getactivewindow", "getwindowname")
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()

	if err != nil {
		log.Fatal(err)
		return
	}

	title := strings.TrimSpace(out.String())

	if _, err := db.Exec("INSERT INTO windows (title, timestamp) VALUES (?, ?)", title, time.Now()); err != nil {
		log.Fatal(err)
	}
}

func startScheduler(interval time.Duration) {
	if activityTicker != nil {
		return
	}

	println("Starting scheduler")

	go func() {
		activityTicker = time.NewTicker(interval)
		activityTickerDone = make(chan struct{})
		for {
			select {
			case <-activityTicker.C:
				saveScreenshots()
				saveActiveWindows()
			case <-activityTickerDone:
				activityTicker.Stop()
				activityTicker = nil
				activityTickerDone = nil
				return
			}
		}
	}()
}

func stopScheduler() {
	if activityTicker == nil {
		return
	}

	println("Stopping scheduler")

	close(activityTickerDone)
}

func getIdleTime() time.Duration {
	// Execute `xprintidle` command
	cmd := exec.Command("xprintidle")
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()

	if err != nil {
		log.Fatal(err)
		return 0
	}

	idleTime := strings.TrimSpace(out.String())
	idleTimeInt, err := strconv.Atoi(idleTime)

	if err != nil {
		log.Fatal(err)
		return 0
	}

	return time.Duration(idleTimeInt) * time.Millisecond
}

// @TODO: notify user when inactivity is detected and prompt to continue
func saveInactivityStart() int64 {
	// Save in database
	result, err := db.Exec("INSERT INTO inactivity (start) VALUES (?)", time.Now())

	if err != nil {
		log.Fatal(err)
		return -1
	}

	id, err := result.LastInsertId()

	if err != nil {
		log.Fatal(err)
		return -1
	}

	return id
}

func saveInactivityEnd(id int64) {
	_, err := db.Exec("UPDATE inactivity SET end = ? WHERE id = ?", time.Now(), id)

	if err != nil {
		log.Fatal(err)
	}
}

type WatchForInactivityParams struct {
	Threshold time.Duration
	IsIdle    func(chan bool)
	OnActive  func()
}

// @TODO: UI for adding reason for inactivity
func watchForInactivity(params WatchForInactivityParams) {
	if idleTicker != nil {
		return
	}

	println("Watching for inactivity")

	threshold := params.Threshold
	isIdle := params.IsIdle
	onActive := params.OnActive

	go func() {
		idleTicker = time.NewTicker(1 * time.Second)
		idleTickerDone = make(chan struct{})
		inactive := getIdleTime() >= threshold

		inactivityId := int64(-1)

		for {
			select {
			case <-idleTicker.C:
				idleTime := getIdleTime()
				if idleTime >= threshold && !inactive {
					println("Confirming inactivity")

					confirmIsIdle := make(chan bool)
					isIdle(confirmIsIdle)

					if <-confirmIsIdle {
						println("Confirmed inactivity")
						inactive = true
						inactivityId = saveInactivityStart()
					} else {
						println("Still active")
					}
				} else if idleTime < threshold && inactive {
					println("Activity detected")
					inactive = false

					if inactivityId != -1 {
						saveInactivityEnd(inactivityId)
					}

					onActive()
				}
			case <-idleTickerDone:
				idleTicker.Stop()
				idleTicker = nil
				idleTickerDone = nil
				return
			}
		}
	}()
}

func stopWatchingForInactivity() {
	if idleTicker == nil {
		return
	}

	println("Stopping watching for inactivity")

	close(idleTickerDone)
}

const PRESENCE_YES = "Yes"
const PRESENCE_NO = "No"
const PRESENCE_TIMEOUT = "Timeout"
const PRESENCE_CLOSED = "Closed"

func askStillWorking(app fyne.App, timeout time.Duration, onResponse func(string)) {
	title := "Are you still working?"
	window := app.NewWindow(title)
	window.Resize(fyne.NewSize(300, 100))
	window.CenterOnScreen()
	window.SetFixedSize(true)

	var presence string

	window.SetOnClosed(func() {
		if presence == "" {
			presence = PRESENCE_CLOSED
		}
		onResponse(presence)
	})

	window.SetContent(container.NewVBox(
		widget.NewLabel(title),
		widget.NewLabel("Automatically closing in "+timeout.String()),
		container.NewHBox(
			layout.NewSpacer(),
			widget.NewButton("Yes", func() {
				presence = PRESENCE_YES
				window.Close()
			}),
			widget.NewButton("No", func() {
				presence = PRESENCE_NO
				window.Close()
			}),
		),
	))

	window.Show()

	// Close window after timeout
	go func() {
		time.Sleep(timeout)
		presence = PRESENCE_TIMEOUT
		window.Close()
	}()
}
