package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

type FileChangeEvent struct {
	Filename string
	Content  []byte
}

// createTempFile creates a temp file and populates it with the provided string
func createTempFile(text string) (string, error) {
	tempFile, err := os.CreateTemp("", "*"+cli.Extension)
	if err != nil {
		return "", err
	}
	if _, err := tempFile.Write([]byte(text)); err != nil {
		return tempFile.Name(), err
	}
	if err := tempFile.Close(); err != nil {
		return tempFile.Name(), err
	}
	return tempFile.Name(), nil
}

// openEditor opens a specified file in an editor (attached to current terminal)
func openEditor(editor string, fn string) error {
	cmd := exec.Command(editor, fn)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// openEditorInTerminal opens the editor in a new terminal window (for daemon mode).
// On macOS, app-based terminals (Ghostty, kitty, etc.) are launched via `open -nWa`
// to avoid IPC issues with already-running instances. Terminal.app uses AppleScript.
func openEditorInTerminal(terminal string, editor string, fn string) error {
	switch terminal {
	case "Terminal":
		// macOS Terminal.app needs AppleScript; blocks until the tab is no longer busy
		script := fmt.Sprintf(`tell application "Terminal"
	activate
	set w to do script "%s %s"
	repeat
		delay 0.5
		if not busy of w then exit repeat
	end repeat
end tell`, editor, fn)
		cmd := exec.Command("osascript", "-e", script)
		return cmd.Run()
	default:
		// Use `open -nWa` to launch a fresh instance and wait for it to exit.
		// -n = new instance (avoids client-server IPC issues with Ghostty, etc.)
		// -W = block until the app exits
		// --args passes the remaining flags to the terminal app
		cmd := exec.Command("open", "-nWa", terminal, "--args", "-e", editor, fn)
		return cmd.Run()
	}
}

// watchFile watches a temp file for changes
// and sends the file's content into the channel
func watchFile(fn string, c chan<- FileChangeEvent) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Error creating file watcher: %v", err)
		return
	}
	defer watcher.Close()

	dir := filepath.Dir(fn)

	// Add directory (not the specific file) to watcher
	// This is because the file is not *changed* when saved by
	// the editor - it is *replaced*, using a temporary file
	err = watcher.Add(dir)
	if err != nil {
		log.Printf("Error adding %s to watcher: %v", dir, err)
		return
	}

	log.Printf("Watching folder %s for changes to %s", dir, fn)

	for {
		select {
		case event := <-watcher.Events:
			if event.Name != fn {
				break
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				// Ignore other operations like Remove, Rename, Chmod, etc.
				break
			}
			// log.Printf("Created/written: %s", event.Name)

			content, err := readFileWhenReady(event.Name, 500*time.Millisecond)
			if err != nil {
				log.Println("Error reading file: ", err)
				continue
			}
			c <- FileChangeEvent{
				Filename: event.Name,
				Content:  content,
			}
		case err, ok := <-watcher.Errors:
			// just in case, I guess
			if !ok {
				return
			}
			log.Println("Watcher error: ", err)
		}
	}
}

// readFileWhenReady keeps trying to read a file that could otherwise
// generate a "not found" error. This seems to be necessary because
// of the way that editors like helix and vim save their files.
func readFileWhenReady(path string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data, nil
		}
		if !os.IsNotExist(err) {
			return nil, err // fail fast on other errors
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("file %s did not appear in time", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
