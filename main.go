package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
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

func main() {
	started = false

	app := app.New()
	window := app.NewWindow("GoTracker")

	window.Resize(fyne.NewSize(200, 200))
	window.SetFixedSize(true)

	var toggleBtn *widget.Button

	toggleBtn = widget.NewButton("Start", func() {
		if started {
			started = false
			toggleBtn.SetText("Start")
			saveSessionEnd(sessionID)
			stopScheduler()
			stopWatchingForInactivity()
		} else {
			started = true
			toggleBtn.SetText("Stop")

			prepareDatabase()

			sessionID = saveSessionStart()

			if sessionID == -1 {
				panic("Failed to start session")
			}

			startScheduler(5 * time.Second)
			watchForInactivity(10*time.Second, 5*time.Second)
		}
	})

	content := container.New(layout.NewCenterLayout(), toggleBtn)

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
			println("for startScheduler")
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

func watchForInactivity(threshold time.Duration, schedulerInterval time.Duration) {
	if idleTicker != nil {
		return
	}

	println("Watching for inactivity")

	go func() {
		idleTicker = time.NewTicker(1 * time.Second)
		idleTickerDone = make(chan struct{})
		inactive := getIdleTime() >= threshold

		inactiveId := int64(-1)

		for {
			println("for watchForInactivity")
			select {
			case <-idleTicker.C:
				idleTime := getIdleTime()
				if idleTime >= threshold && !inactive {
					inactive = true
					println("User went inactive")
					inactiveId = saveInactivityStart()
					stopScheduler()
				} else if idleTime < threshold && inactive {
					inactive = false
					println("User is active")
					if inactiveId != -1 {
						saveInactivityEnd(inactiveId)
					}
					startScheduler(schedulerInterval)
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
