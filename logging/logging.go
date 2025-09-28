package logging

import "log"

// Debug controls whether debug-level logs are printed.
var Debug bool

// SetDebug sets the global debug flag.
func SetDebug(d bool) { Debug = d }

// Debugf prints formatted debug logs when debug is enabled.
func Debugf(format string, v ...any) {
    if Debug {
        log.Printf(format, v...)
    }
}

// Debugln prints debug logs when debug is enabled.
func Debugln(v ...any) {
    if Debug {
        log.Println(v...)
    }
}

// Errorf prints error logs only when debug is enabled.
// Use for high-frequency errors that should be quiet by default.
func Errorf(format string, v ...any) {
    if Debug {
        log.Printf(format, v...)
    }
}