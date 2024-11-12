package main

/*
#cgo LDFLAGS: -lX11
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <stdlib.h>

// Function to get window IDs from _NET_CLIENT_LIST
Window* getOpenWindows(Display* display, Atom clientListAtom, unsigned long* len) {
    Atom actualType;
    int actualFormat;
    unsigned long bytesAfter;
    unsigned char *prop = NULL;

    Window root = DefaultRootWindow(display);
    if (XGetWindowProperty(display, root, clientListAtom, 0, ~0, False, AnyPropertyType,
                           &actualType, &actualFormat, len, &bytesAfter, &prop) != Success) {
        return NULL;
    }

    return (Window*)prop;
}

// Function to get the window name from _NET_WM_NAME
char* getWindowName(Display* display, Window window, Atom nameAtom) {
    Atom actualType;
    int actualFormat;
    unsigned long numItems, bytesAfter;
    unsigned char *prop = NULL;

    if (XGetWindowProperty(display, window, nameAtom, 0, (~0L), False, AnyPropertyType,
                           &actualType, &actualFormat, &numItems, &bytesAfter, &prop) != Success) {
        return NULL;
    }
    return (char*)prop;
}

// Function to get the active window from _NET_ACTIVE_WINDOW
Window getActiveWindow(Display* display, Atom activeWindowAtom) {
    Atom actualType;
    int actualFormat;
    unsigned long numItems, bytesAfter;
    unsigned char *prop = NULL;

    Window root = DefaultRootWindow(display);
    if (XGetWindowProperty(display, root, activeWindowAtom, 0, ~0, False, AnyPropertyType,
                           &actualType, &actualFormat, &numItems, &bytesAfter, &prop) != Success) {
        return 0;
    }

    Window activeWindow = prop ? *((Window*)prop) : 0;
    XFree(prop);
    return activeWindow;
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// WindowInfo holds the name and active status of a window
type WindowInfo struct {
	Name   string
	Active bool
}

// getWindows retrieves the names and active status of all open windows.
func getWindows() ([]WindowInfo, error) {
	// Open a connection to the X server
	display := C.XOpenDisplay(nil)
	if display == nil {
		return nil, fmt.Errorf("unable to open X display")
	}
	defer C.XCloseDisplay(display)

	// Get atoms for _NET_CLIENT_LIST, _NET_WM_NAME, and _NET_ACTIVE_WINDOW
	clientListAtom := C.XInternAtom(display, C.CString("_NET_CLIENT_LIST"), C.False)
	nameAtom := C.XInternAtom(display, C.CString("_NET_WM_NAME"), C.False)
	activeWindowAtom := C.XInternAtom(display, C.CString("_NET_ACTIVE_WINDOW"), C.False)
	var length C.ulong

	// Get the active window ID
	activeWindowID := C.getActiveWindow(display, activeWindowAtom)

	// Get the list of open window IDs
	windowIDs := C.getOpenWindows(display, clientListAtom, &length)
	if windowIDs == nil {
		return nil, fmt.Errorf("unable to retrieve open windows")
	}
	defer C.XFree(unsafe.Pointer(windowIDs))

	// Convert C array to Go slice
	windows := (*[1 << 20]C.Window)(unsafe.Pointer(windowIDs))[:length:length]

	// Slice to store window information
	windowInfos := make([]WindowInfo, 0, length)

	// Iterate over each window ID to get the window name and active status
	for _, win := range windows {
		// Get the window name
		name := C.getWindowName(display, win, nameAtom)
		windowName := "[Unnamed]"
		if name != nil {
			windowName = C.GoString(name)
			C.XFree(unsafe.Pointer(name)) // Free the name after use
		}

		// Determine if this window is active
		isActive := win == activeWindowID
		windowInfos = append(windowInfos, WindowInfo{Name: windowName, Active: isActive})
	}

	return windowInfos, nil
}
