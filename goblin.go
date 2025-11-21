package main

import (
	"bufio"
	"fmt"
	"go/format"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"strconv"

	"regexp"

	"github.com/chzyer/readline"
	"github.com/fatih/color"
	"golang.org/x/term"

	"goblin.go/version"
)

var originalTerminalState *term.State

// getGoVersion returns the Go version string.
func getGoVersion() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// Color definitions
var (
	errorColor   = color.New(color.FgRed).SprintfFunc()
	successColor = color.New(color.FgGreen).SprintfFunc()
	infoColor    = color.New(color.FgYellow).SprintfFunc()
	outputColor  = color.New(color.FgCyan).SprintFunc()
	snippetColor = color.New(color.FgMagenta).SprintFunc()
)

// REPL_SAVES_DIR is the directory where code snippets will be saved and loaded from.
var REPL_SAVES_DIR = filepath.Join(os.Getenv("HOME"), ".goblin", "snippets")

// HISTORY_FILE is the path to the command history file.
var HISTORY_FILE = filepath.Join(os.Getenv("HOME"), ".goblin", "history")

// lastLoadedFilePath stores the path of the last file loaded using :load.
var lastLoadedFilePath string

// currentSnippetName stores the name of the currently active snippet (without extension).
var currentSnippetName string

// bufferDirty tracks whether the content of the code buffer has changed since the last save.
var bufferDirty bool

// promptToSave checks if the buffer is dirty and asks the user to save.
// It returns true if the calling action (e.g., exit, load) should proceed, false otherwise.
func promptToSave(rl *readline.Instance, code string) bool {
	if !bufferDirty {
		return true // Not dirty, proceed
	}

	rl.SetPrompt(infoColor("Current snippet has unsaved changes. Save now? (y/n) "))
	answer, err := rl.Readline()
	if err != nil {
		fmt.Println(errorColor("\nOperation cancelled."))
		return false // Error reading line, cancel action
	}

	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "y" || answer == "yes" {
		handleSave(code, []string{}) // Save with default name
		return true                  // Proceed after saving
	} else if answer == "n" || answer == "no" {
		return true // Proceed without saving
	}

	fmt.Println(errorColor("Invalid input. Operation cancelled."))
	return false // Invalid input, cancel action
}

// initConfig ensures the configuration directory and necessary subdirectories exist.
func initConfig() {
	// Create the main ~/.goblin directory
	if err := os.MkdirAll(filepath.Dir(REPL_SAVES_DIR), 0755); err != nil {
		color.New(color.FgRed).Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}
	// Create the snippets subdirectory
	if err := os.MkdirAll(REPL_SAVES_DIR, 0755); err != nil {
		color.New(color.FgRed).Fprintf(os.Stderr, "Error creating snippets directory: %v\n", err)
		os.Exit(1)
	}
}

// codeTemplate provides the Go program structure.
// It separates top-level declarations from statements that run in main.
const codeTemplate = `
package main

import (
	"fmt"
%s // User-provided imports
)

%s // Global variables, constants, types, and functions

func main() {
%s // Statements
}
`

func separateCodeParts(code string) (userImports, topLevelDeclarations, statements string) {
	var userImportsBuilder, topLevelDeclarationsBuilder, statementsBuilder strings.Builder
	lines := strings.Split(code, "\n")

	// Regex for identifying different code constructs
	importSingleRegex := regexp.MustCompile(`^import\s+(\"?[\w/.]+\"?)$`)
	importGroupRegex := regexp.MustCompile(`^import\s*\($`)
	globalDeclStartRegex := regexp.MustCompile(`^(var|const|type)\s+`)
	funcDeclStartRegex := regexp.MustCompile(`^func\s+`)

	inImportBlock := false
	inGlobalDeclBlock := false // For multi-line var/const/type blocks
	inFuncDecl := false
	braceCount := 0

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Skip empty lines at the top level, they don't affect parsing logic
		if trimmedLine == "" && !inImportBlock && !inGlobalDeclBlock && !inFuncDecl {
			continue
		}

		// --- Handle Import Blocks ---
		if importGroupRegex.MatchString(trimmedLine) {
			inImportBlock = true
			braceCount = 1 // Start of import block
			continue       // Do not write "import (" to userImportsBuilder
		}
		if inImportBlock {
			braceCount += strings.Count(line, "(")
			braceCount -= strings.Count(line, ")")
			if braceCount <= 0 { // End of import block
				inImportBlock = false
				braceCount = 0 // Reset brace count
				continue       // Do not write ")" to userImportsBuilder
			}
			// This is an import path within a group
			userImportsBuilder.WriteString(line + "\n")
			continue
		}
		if matches := importSingleRegex.FindStringSubmatch(trimmedLine); len(matches) > 1 {
			// This is a single-line import, extract the path and format it
			userImportsBuilder.WriteString("\t" + matches[1] + "\n")
			continue
		}

		// --- Handle Global Declarations (var, const, type) ---
		if !inFuncDecl && !inImportBlock && globalDeclStartRegex.MatchString(trimmedLine) {
			// Check for multi-line var/const/type blocks
			if strings.HasSuffix(trimmedLine, "(") { // e.g., var (
				inGlobalDeclBlock = true
				topLevelDeclarationsBuilder.WriteString(line + "\n")
				braceCount += strings.Count(line, "(")
				braceCount -= strings.Count(line, ")")
				continue
			} else { // Single line var/const/type
				topLevelDeclarationsBuilder.WriteString(line + "\n")
				continue
			}
		}
		if inGlobalDeclBlock {
			topLevelDeclarationsBuilder.WriteString(line + "\n")
			braceCount += strings.Count(line, "(")
			braceCount -= strings.Count(line, ")")
			if braceCount <= 0 {
				inGlobalDeclBlock = false
				braceCount = 0 // Reset brace count
			}
			continue
		}

		// --- Handle Function Declarations ---
		if !inImportBlock && !inGlobalDeclBlock && funcDeclStartRegex.MatchString(trimmedLine) {
			inFuncDecl = true
			topLevelDeclarationsBuilder.WriteString(line + "\n")
			braceCount += strings.Count(line, "{")
			braceCount -= strings.Count(line, "}")
			continue
		}
		if inFuncDecl {
			topLevelDeclarationsBuilder.WriteString(line + "\n")
			braceCount += strings.Count(line, "{")
			braceCount -= strings.Count(line, "}")
			if braceCount <= 0 {
				inFuncDecl = false
				braceCount = 0 // Reset brace count
			}
			continue
		}

		// --- Handle Statements (everything else) ---
		if trimmedLine != "" {
			statementsBuilder.WriteString(line + "\n")
		}
	}

	return userImportsBuilder.String(), topLevelDeclarationsBuilder.String(), statementsBuilder.String()
}

// executeCode takes the accumulated user code, separates declarations from statements,
// wraps them in the template, writes to a temporary file, and executes it.
func executeCode(code string, args []string) (string, error) {
	userImports, topLevelDeclarations, statements := separateCodeParts(code)

	// 1. Fill the template with the separated code
	fullCode := fmt.Sprintf(codeTemplate, userImports, topLevelDeclarations, statements)

	// 2. Create a temporary file to hold the code
	tmpDir, err := ioutil.TempDir("", "gorepl_tmp")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir) // Clean up the directory and contents afterwards

	tmpFilePath := tmpDir + "/repl_code.go"

	// 3. Write code to the temporary file
	if err := ioutil.WriteFile(tmpFilePath, []byte(fullCode), 0644); err != nil {
		return "", fmt.Errorf("failed to write code to temp file: %w", err)
	}

	// 4. Execute the code using 'go run'
	cmdArgs := append([]string{"run", tmpFilePath}, args...)
	cmd := exec.Command("go", cmdArgs...)

	// We keep GOWORK=off to prevent conflicts with Go Workspaces.
	cmd.Env = append(os.Environ(), "GOWORK=off")

	// Capture combined output (stdout and stderr)
	output, err := cmd.CombinedOutput()

	// 5. Check if the 'go run' command itself failed
	if exitErr, ok := err.(*exec.ExitError); ok {
		// Compilation or runtime error happened in the user's code.
		return string(output), exitErr
	}

	return string(output), nil
}

// handleList lists all saved files in the REPL_SAVES_DIR.
func handleList() {
	fmt.Println(infoColor("--- Saved Snippets ---"))
	files, err := ioutil.ReadDir(REPL_SAVES_DIR)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println(infoColor("Save directory '%s' does not exist yet.", REPL_SAVES_DIR))
			return
		}
		fmt.Fprintln(os.Stderr, errorColor("Error listing files: %v", err))
		return
	}

	if len(files) == 0 {
		fmt.Println(infoColor("No saved files found."))
		return
	}

	for _, file := range files {
		if !file.IsDir() {
			fmt.Printf("> %s (%d bytes)\n", file.Name(), file.Size())
		}
	}
	fmt.Println(infoColor("----------------------"))
}

// ensureGoExtension checks if the filename has a .go extension and adds it if missing.
func ensureGoExtension(filename string) string {
	if !strings.HasSuffix(filename, ".go") {
		return filename + ".go"
	}
	return filename
}

// handleSave saves the current code buffer to the specified filename.
func handleSave(code string, args []string) {
	filename := ""

	if len(args) == 0 {
		if currentSnippetName != "" {
			filename = ensureGoExtension(currentSnippetName)
			fmt.Println(infoColor("No filename provided. Saving to current snippet: '%s'", filename))
		} else if lastLoadedFilePath != "" {
			filename = ensureGoExtension(filepath.Base(lastLoadedFilePath))
			fmt.Println(infoColor("No filename provided. Saving to last loaded file: '%s'", filename))
		} else {
			// Generate a random filename based on timestamp
			filename = fmt.Sprintf("snippet_%s.go", time.Now().Format("20060102_150405"))
			fmt.Println(infoColor("No filename provided and no previous file loaded. Saving to new file: '%s'", filename))
		}
	} else if len(args) >= 1 {
		filename = ensureGoExtension(strings.Join(args, " "))
	}

	currentSnippetName = strings.TrimSuffix(filename, ".go")

	// 1. Write the code to the file
	filePath := filepath.Join(REPL_SAVES_DIR, filename)
	if err := ioutil.WriteFile(filePath, []byte(code), 0644); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error saving code to '%s': %v", filename, err))
		return
	}

	fmt.Println(successColor("Code successfully saved to '%s'.", filePath))
}

// handleLoad loads the specified filename into the current code buffer.
func handleLoad(codeLines *[]string, args []string) {
	if len(args) != 1 {
		fmt.Println(infoColor("Usage: :load <filename>"))
		return
	}
	filename := ensureGoExtension(args[0])
	filePath := filepath.Join(REPL_SAVES_DIR, filename)

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error loading file '%s': %v", filename, err))
		return
	}

	// Clear and set the new content
	*codeLines = strings.Split(string(data), "\n")

	lastLoadedFilePath = filePath // Store the last loaded file path
	currentSnippetName = strings.TrimSuffix(filepath.Base(filePath), ".go")

	fmt.Println(successColor("Code successfully loaded from '%s'. Buffer reset and updated.", filePath))
}

// handleExport exports the current code buffer to a full Go source file.
func handleExport(code string, args []string) {
	outputPath := ""

	if len(args) == 0 {
		filename := ""
		if lastLoadedFilePath != "" {
			filename = ensureGoExtension(filepath.Base(lastLoadedFilePath))
			fmt.Println(infoColor("No filename provided. Exporting to last loaded file name: '%s' in home directory.", filename))
		} else {
			filename = fmt.Sprintf("snippet_%s.go", time.Now().Format("20060102_150405"))
			fmt.Println(infoColor("No filename provided and no previous file loaded. Exporting to new file: '%s' in home directory.", filename))
		}
		outputPath = filepath.Join(os.Getenv("HOME"), filename)
	} else if len(args) >= 1 {
		outputPath = ensureGoExtension(strings.Join(args, " "))
	} else {
		fmt.Println(infoColor("Usage: :export [<filepath>]"))
		return
	}

	// Separate code parts
	userImports, topLevelDeclarations, statements := separateCodeParts(code)

	// Fill the template with the separated code
	fullCode := fmt.Sprintf(codeTemplate, userImports, topLevelDeclarations, statements)

	// Ensure the directory exists
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error creating directory '%s': %v", dir, err))
		return
	}

	// Write the code to the file
	if err := ioutil.WriteFile(outputPath, []byte(fullCode), 0644); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error exporting code to '%s': %v", outputPath, err))
		return
	}

	fmt.Println(successColor("Code successfully exported to '%s'.", outputPath))
}

// handleSaveAs saves the current code buffer to a new file with the specified name,
// and then sets this new file as the currently active snippet.
func handleSaveAs(code string, args []string) {
	if len(args) != 1 {
		fmt.Println(infoColor("Usage: :saveas <new_filename>"))
		return
	}

	newFilename := ensureGoExtension(args[0])
	newFilePath := filepath.Join(REPL_SAVES_DIR, newFilename)

	// Check if the new file name already exists
	if _, err := os.Stat(newFilePath); err == nil {
		fmt.Fprintln(os.Stderr, errorColor("Error: A snippet named '%s' already exists. Choose a different name.", newFilename))
		return
	}

	// Write the current code to the new file
	if err := ioutil.WriteFile(newFilePath, []byte(code), 0644); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error saving code to '%s': %v", newFilename, err))
		return
	}

	// Update the current snippet to the new file
	lastLoadedFilePath = newFilePath
	currentSnippetName = strings.TrimSuffix(newFilename, ".go")

	fmt.Println(successColor("Code successfully saved as '%s'. Current snippet is now '%s'.", newFilename, currentSnippetName))
}

// handleRename renames the current code buffer's associated file.
func handleRename(args []string) {
	if len(args) != 1 {
		fmt.Println(infoColor("Usage: :rename <new_filename>"))
		return
	}

	if lastLoadedFilePath == "" {
		fmt.Println(infoColor("No snippet is currently loaded or saved to rename. Use :save first."))
		return
	}

	oldFilePath := lastLoadedFilePath
	newFilename := ensureGoExtension(args[0])
	newFilePath := filepath.Join(REPL_SAVES_DIR, newFilename)

	// Check if the new file name already exists
	if _, err := os.Stat(newFilePath); err == nil {
		fmt.Fprintln(os.Stderr, errorColor("Error: A snippet named '%s' already exists. Choose a different name.", newFilename))
		return
	}

	if err := os.Rename(oldFilePath, newFilePath); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error renaming snippet from '%s' to '%s': %v", filepath.Base(oldFilePath), newFilename, err))
		return
	}

	lastLoadedFilePath = newFilePath
	currentSnippetName = strings.TrimSuffix(newFilename, ".go")
	fmt.Println(successColor("Snippet successfully renamed to '%s'.", newFilename))
}

// handleTidy formats the current code buffer using go/format.
func handleTidy(code string) ([]string, error) {
	// A local template for formatting. Comments are removed to prevent them
	// from being inserted into the buffer.
	const codeTemplateForTidy = `
package main

import (
%s
)

%s

func main() {
%s
}
`
	// 1. Assemble the code into a valid Go program using the local template.
	userImports, topLevelDeclarations, statements := separateCodeParts(code)
	fullCode := fmt.Sprintf(codeTemplateForTidy, userImports, topLevelDeclarations, statements)

	// 2. Format the entire program's source code.
	formattedSource, err := format.Source([]byte(fullCode))
	if err != nil {
		return nil, err
	}

	// 3. Parse the formatted code back into its constituent parts.
	// We ignore the 'statements' part of the output, as it will incorrectly contain "package main".
	formattedImports, formattedTopLevel, _ := separateCodeParts(string(formattedSource))

	// 4. The separateCodeParts function incorrectly puts the entire main function
	// into formattedTopLevel. We need to extract the statements from it.
	var formattedStatements string
	mainFuncRegex := regexp.MustCompile(`(?s)func main\(\) \{\n?(.*)\n\s*\}`)
	matches := mainFuncRegex.FindStringSubmatch(formattedTopLevel)

	if len(matches) > 1 {
		// The captured content of main becomes our statements.
		formattedStatements = strings.Trim(matches[1], "\n")
		// Remove the main function from formattedTopLevel.
		formattedTopLevel = mainFuncRegex.ReplaceAllString(formattedTopLevel, "")
	}

	// 5. Reconstruct the buffer by concatenating the formatted parts with proper spacing.
	var finalParts []string

	// Handle imports
	importLines := strings.Split(strings.TrimSpace(formattedImports), "\n")
	if len(importLines) > 0 && importLines[0] != "" {
		var importBlock string
		if len(importLines) == 1 {
			importBlock = "import " + strings.TrimSpace(importLines[0])
		} else {
			importBlock = "import (\n" + strings.Join(importLines, "\n") + "\n)"
		}
		finalParts = append(finalParts, importBlock)
	}

	// Handle top-level declarations
	cleanedTopLevel := strings.TrimSpace(formattedTopLevel)
	if cleanedTopLevel != "" {
		finalParts = append(finalParts, cleanedTopLevel)
	}

	// Handle statements
	cleanedStatements := strings.TrimSpace(formattedStatements)
	if cleanedStatements != "" {
		finalParts = append(finalParts, cleanedStatements)
	}

	// Join the parts with appropriate spacing
	finalBufferContent := strings.Join(finalParts, "\n\n")

	if finalBufferContent == "" {
		return []string{}, nil
	}
	return strings.Split(finalBufferContent, "\n"), nil
}

// handleEdit opens the current code buffer in an external editor.
func handleEdit(codeLines *[]string) {
	// 1. Create a temporary file with a .go extension
	tmpfile, err := ioutil.TempFile("", "goblin-*.go")
	if err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error creating temporary file: %v", err))
		return
	}
	defer os.Remove(tmpfile.Name()) // Clean up the file afterwards

	// 2. Write the current buffer to the temporary file
	if _, err := tmpfile.WriteString(strings.Join(*codeLines, "\n")); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error writing to temporary file: %v", err))
		return
	}
	if err := tmpfile.Close(); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error closing temporary file: %v", err))
		return
	}

	// 3. Get the user's preferred editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		// Fallback to a common default if EDITOR is not set
		editor = "nano"
	}

	// 4. Open the file in the editor
	cmd := exec.Command(editor, tmpfile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error opening editor '%s': %v", editor, err))
		return
	}

	// 5. Read the modified content back into the buffer
	data, err := ioutil.ReadFile(tmpfile.Name())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorColor("Error reading modified file: %v", err))
		return
	}

	// 6. Reset the buffer and write the new content
	*codeLines = strings.Split(string(data), "\n")

	fmt.Println(successColor("Buffer updated from editor."))

}

// handleSys executes a system command with real-time output,
// allowing interruption with the Escape key without killing the main REPL process.
func handleSys(args []string, rl *readline.Instance) (error, bool) {
	// Clear readline's buffer before going into raw mode
	rl.Clean()

	// Set raw mode for capturing individual key presses
	if err := setRawMode(); err != nil {
		return fmt.Errorf("Failed to set raw terminal mode: %w", err), true // Reinit readline on failure
	}
	defer restoreMode() // Ensure terminal is restored on exit from this function.

	if len(args) == 0 {
		return fmt.Errorf("Usage: :sys <command> [args...] "), false
	}

	// --- Command Setup ---
	cmd := exec.Command(args[0], args[1:]...)
	// Create a new process group for the command. This is essential for signal handling.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Get pipes for stdout and stderr to stream output in real-time.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("Error creating stdout pipe: %w", err), true // Reinit readline on failure
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("Error creating stderr pipe: %w", err), true // Reinit readline on failure
	}

	// --- Start Command ---
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Error starting command: %w", err), true // Reinit readline on failure
	}

	// Channels for key press listener
	escapePressedChan := make(chan struct{}, 1)
	stopKeyListenerChan := make(chan struct{}, 1)
	keyListenerStoppedChan := make(chan struct{}, 1)

	go keyPressListener(escapePressedChan, stopKeyListenerChan, keyListenerStoppedChan)

	// --- Goroutines for Real-time Output Streaming ---
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			fmt.Fprintf(os.Stdout, "%s\r\n", successColor(scanner.Text()))
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			fmt.Fprintf(os.Stderr, "%s\r\n", errorColor(scanner.Text()))
		}
	}()

	// Set up a channel to signal when the command has completed.
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
	}()

	shouldReinitializeReadline := true

	// --- Main Event Loop ---
	select {
	case err := <-cmdDone:
		// Command finished on its own.
		if err != nil {
			// This will report errors like non-zero exit statuses.
			// It is generally expected and can be ignored if not needed.
		}
	case <-escapePressedChan:
		fmt.Println(infoColor("\nEscape pressed. Terminating system command..."))
		if cmd.Process != nil {
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
				fmt.Fprintln(os.Stderr, errorColor("Failed to terminate command: %v", err))
			}
		}
		// Wait for the command to actually finish after being signaled.
		<-cmdDone
	}

	// Signal key listener to stop immediately after command outcome is known.
	close(stopKeyListenerChan)
	// Wait for the key listener goroutine to confirm it has stopped.
	<-keyListenerStoppedChan

	// Wait for the output streaming goroutines to finish to ensure all output is flushed.
	wg.Wait()

	// Restore terminal to cooked mode *before* readline processes further input.
	restoreMode()

	// Ensure the prompt starts on a new line
	fmt.Fprint(os.Stdout, "\r\n")

	return nil, shouldReinitializeReadline
}

// setRawMode puts the terminal into raw mode.
func setRawMode() error {
	var err error
	originalTerminalState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw terminal mode: %w", err)
	}
	return nil
}

// restoreMode restores the terminal to its original cooked mode.
func restoreMode() {
	if originalTerminalState != nil {
		fd := int(os.Stdin.Fd())
		term.Restore(fd, originalTerminalState)
		originalTerminalState = nil // Clear the state after restoring
	}
}

// readKey reads a single key press from stdin in raw mode.
// It will block until a key is pressed.
func readKey() (rune, error) {
	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return rune(buf[0]), nil
}

func keyPressListener(escapePressedChan chan<- struct{}, stopKeyListenerChan <-chan struct{}, keyListenerStoppedChan chan<- struct{}) {
	defer func() { keyListenerStoppedChan <- struct{}{} }() // Signal that listener is stopped

	// Set stdin to non-blocking mode to allow polling.
	fd := int(os.Stdin.Fd())
	oldFlags, _, err := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if err != 0 {
		return // Cannot proceed without fcntl.
	}
	defer syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, oldFlags) // Ensure flags are restored.

	if _, _, err := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, oldFlags|syscall.O_NONBLOCK); err != 0 {
		return // Cannot proceed if non-blocking mode fails.
	}

	var buf [1]byte
	for {
		select {
		case <-stopKeyListenerChan:
			return // The command has finished, so we stop listening.
		default:
			// Try to read from stdin.
			n, readErr := syscall.Read(fd, buf[:])

			if n > 0 && buf[0] == 27 { // Escape key
				select {
				case escapePressedChan <- struct{}{}:
				default:
				}
				return // Stop listening after escape is pressed.
			}

			// If there was an error, check if it was EAGAIN (no data available).
			if readErr == syscall.EAGAIN || readErr == syscall.EWOULDBLOCK {
				// No data, wait a bit before polling again to avoid busy-waiting.
				time.Sleep(50 * time.Millisecond)
				continue
			} else if readErr != nil {
				// An actual error occurred.
				return
			}
		}
	}
}

// handleHelp displays a list of available commands.
func handleHelp() {

	fmt.Println(infoColor("\n🐗 Goblin %s - Commands summary :", version.String()))
	fmt.Println(":run [args...]           - Execute the current Go code in the buffer with optional arguments.")
	fmt.Println(":sys <command> [args...] - Execute a system command.")
	fmt.Println(":clear                   - Clear the current code buffer.")
	fmt.Println(":show                    - Display the current content of the code buffer.")
	fmt.Println(":tidy                    - Format the code in the buffer.")
	fmt.Println(":list                    - List all saved code snippets.")
	fmt.Println(":save <file>             - Save the current code buffer to a file.")
	fmt.Println(":saveas <file>           - Save the current buffer to a new file and make it the active snippet.")
	fmt.Println(":load <file>             - Load code from a file into the buffer, replacing current content.")
	fmt.Println(":rename <new_name>       - Rename the current snippet.")
	fmt.Println(":export <filepath>       - Export the current code buffer to a full Go source file.")
	fmt.Println(":edit                    - Open the current code buffer in an external editor for modification.")
	fmt.Println(":u(ndo)                  - Remove the last entry from the buffer.")
	fmt.Println(":d(elete) <line>         - Delete a specific line from the buffer by its number.")
	fmt.Println(":i(nsert) <line>         - Insert an empty line before the provided line number.")
	fmt.Println(":help                    - Display this help message.")
	fmt.Println(":q(uit), :exit, :bye     - Exit the REPL.")
	fmt.Println()
}

func updatePrompt(rl *readline.Instance) {
	if currentSnippetName != "" {
		dirtyIndicator := ""
		if bufferDirty {
			dirtyIndicator = "*"
		}
		rl.SetPrompt(fmt.Sprintf("[%s%s]go> ", snippetColor(currentSnippetName), dirtyIndicator))
	} else {
		rl.SetPrompt("go> ")
	}
}

func main() {
	// Defer the restoration of the terminal to ensure it's always reset on exit.
	defer restoreMode()

	initConfig() // Ensure ~/.goblin exists

	fmt.Println(infoColor("🐗 Goblin %s - An enhanced REPL for Go.", version.String()))
	fmt.Println(infoColor("%s", getGoVersion()))
	fmt.Println(infoColor("Enter Go statements and type ':run' to execute."))
	fmt.Println(infoColor("Type 'fmt.Println(...)' to display results."))
	fmt.Println(infoColor("Type ':help' to see the available commands."))
	fmt.Println()

	var codeLines []string
	var nextInputReplacesLine = 0 // 0 means append, > 0 means replace line number
	currentSnippetName = ""
	bufferDirty = false

	rlConfig := &readline.Config{
		Prompt:      "go> ",
		HistoryFile: HISTORY_FILE,
	}
	rl, err := readline.NewEx(rlConfig)
	if err != nil {
		panic(err)
	}

	updatePrompt(rl)

	for {
		// Set prompt based on mode (insert vs. normal)
		if nextInputReplacesLine > 0 {
			rl.SetPrompt(fmt.Sprintf("%4d> ", nextInputReplacesLine))
		}

		// Read line input
		input, err := rl.Readline()
		if err != nil { // io.EOF, readline.ErrInterrupt
			if !promptToSave(rl, strings.Join(codeLines, "\n")) {
				updatePrompt(rl)
				continue
			}
			fmt.Println(infoColor("\nExiting Goblin REPL."))
			rl.Close()
			break
		}

		// If in replace mode and user enters empty line, consider it "done"
		if nextInputReplacesLine > 0 && strings.TrimSpace(input) == "" {
			fmt.Printf("Line %d remains empty.\n", nextInputReplacesLine)
			nextInputReplacesLine = 0
			updatePrompt(rl)
			continue
		}

		line := strings.TrimSpace(input)
		fields := strings.Fields(line) // Split input into command and arguments

		// If not a command and in replace mode, replace the line content
		isCommand := len(fields) > 0 && strings.HasPrefix(fields[0], ":")
		if nextInputReplacesLine > 0 && !isCommand {
			if codeLines[nextInputReplacesLine-1] != input {
				bufferDirty = true
			}
			codeLines[nextInputReplacesLine-1] = input
			fmt.Printf("Line %d updated.\n", nextInputReplacesLine)
			nextInputReplacesLine = 0
			updatePrompt(rl)
			continue
		}

		if len(fields) == 0 {
			continue // Skip empty line
		}

		cmd := fields[0]
		args := fields[1:]

		// --- Handle REPL Commands ---
		switch cmd {
		case ":quit", ":exit", ":bye", ":q":
			if !promptToSave(rl, strings.Join(codeLines, "\n")) {
				updatePrompt(rl)
				continue
			}
			fmt.Println(infoColor("\n🐗 Goblin %s - https://github.com/jplozf/goblin", version.String()))
			rl.Close()
			return
		case ":clear":
			if !promptToSave(rl, strings.Join(codeLines, "\n")) {
				updatePrompt(rl)
				continue
			}
			codeLines = []string{}
			currentSnippetName = ""
			lastLoadedFilePath = ""   // Reset the last loaded file path
			nextInputReplacesLine = 0 // Reset insert mode
			bufferDirty = false
			fmt.Println(infoColor("Code buffer cleared."))
			updatePrompt(rl)
			continue
		case ":show":
			if len(codeLines) == 0 {
				fmt.Println(infoColor("Code buffer is empty."))
			} else {
				fmt.Println(infoColor("\n--- Current Code Buffer ---"))
				for i, line := range codeLines {
					fmt.Printf("%4d: %s\n", i+1, line)
				}
				fmt.Println(infoColor("---------------------------"))
			}
			// Do not reset prompt if in insert mode
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":list":
			handleList()
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":save":
			handleSave(strings.Join(codeLines, "\n"), args)
			bufferDirty = false
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":load":
			if !promptToSave(rl, strings.Join(codeLines, "\n")) {
				updatePrompt(rl)
				continue
			}
			handleLoad(&codeLines, args)
			nextInputReplacesLine = 0 // Reset insert mode
			bufferDirty = false
			updatePrompt(rl)
			continue
		case ":export":
			if len(codeLines) == 0 {
				fmt.Println(infoColor("No code in buffer to export."))
				continue
			}
			handleExport(strings.Join(codeLines, "\n"), args)
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":edit":
			handleEdit(&codeLines)
			bufferDirty = true
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":insert", ":i":
			if len(args) != 1 {
				fmt.Println(infoColor("Usage: :insert <line_number>"))
				continue
			}
			lineNum, err := strconv.Atoi(args[0])
			if err != nil || lineNum < 1 || lineNum > len(codeLines)+1 {
				fmt.Fprintln(os.Stderr, errorColor("Invalid line number: %s. Please provide a number between 1 and %d.", args[0], len(codeLines)+1))
				continue
			}
			// Adjust for 0-based indexing
			indexToInsert := lineNum - 1
			codeLines = append(codeLines[:indexToInsert], append([]string{""}, codeLines[indexToInsert:]...)...)
			bufferDirty = true
			fmt.Println(successColor("Empty line inserted at line %d. Enter code at the prompt.", lineNum))
			nextInputReplacesLine = lineNum // Set state for next input
			continue
		case ":rename":
			handleRename(args)
			updatePrompt(rl)
			continue
		case ":saveas":
			if len(codeLines) == 0 {
				fmt.Println(infoColor("No code in buffer to save."))
				continue
			}
			handleSaveAs(strings.Join(codeLines, "\n"), args)
			bufferDirty = false
			updatePrompt(rl)
			continue
		case ":delete", ":d":
			if len(args) != 1 {
				fmt.Println(infoColor("Usage: :delete <line_number>"))
				continue
			}
			lineNum, err := strconv.Atoi(args[0])
			if err != nil || lineNum < 1 || lineNum > len(codeLines) {
				fmt.Fprintln(os.Stderr, errorColor("Invalid line number: %s. Please provide a number between 1 and %d.", args[0], len(codeLines)))
				continue
			}

			// Cancel insert mode if it's affected
			if nextInputReplacesLine > 0 {
				fmt.Println(infoColor("Insert mode cancelled."))
				nextInputReplacesLine = 0
			}

			// Adjust for 0-based indexing
			indexToDelete := lineNum - 1
			codeLines = append(codeLines[:indexToDelete], codeLines[indexToDelete+1:]...)
			bufferDirty = true
			fmt.Println(successColor("Line %d deleted. Current buffer:", lineNum))
			// Re-display the buffer with line numbers
			if len(codeLines) == 0 {
				fmt.Println(infoColor("Code buffer is empty."))
			} else {
				fmt.Println(infoColor("\n--- Current Code Buffer ---"))
				for i, line := range codeLines {
					fmt.Printf("%4d: %s\n", i+1, line)
				}
				fmt.Println(infoColor("---------------------------"))
			}
			updatePrompt(rl)
			continue
		case ":help":
			handleHelp()
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":undo", ":u":
			if len(codeLines) > 0 {
				codeLines = codeLines[:len(codeLines)-1]
				bufferDirty = true
				fmt.Println(successColor("Last entry removed."))
			} else {
				fmt.Println(infoColor("Buffer is empty, nothing to undo."))
			}
			if nextInputReplacesLine == 0 {
				updatePrompt(rl)
			}
			continue
		case ":tidy":
			if len(codeLines) == 0 {
				fmt.Println(infoColor("No code in buffer to tidy."))
				continue
			}
			tidiedLines, err := handleTidy(strings.Join(codeLines, "\n"))
			if err != nil {
				fmt.Fprintln(os.Stderr, errorColor("Error tidying code: %v", err))
				continue
			}
			codeLines = tidiedLines
			bufferDirty = true
			fmt.Println(successColor("Code buffer tidied."))
			// Re-display the buffer with line numbers
			if len(codeLines) == 0 {
				fmt.Println(infoColor("Code buffer is empty."))
			} else {
				fmt.Println(infoColor("\n--- Current Code Buffer ---"))
				for i, line := range codeLines {
					fmt.Printf("%4d: %s\n", i+1, line)
				}
				fmt.Println(infoColor("---------------------------"))
			}
			updatePrompt(rl)
			continue
		case ":run":
			if nextInputReplacesLine > 0 {
				fmt.Println("Cannot run while in insert mode. Finish editing the line first.")
				continue
			}
			// Execute the accumulated code
			if len(codeLines) == 0 {
				fmt.Println("No code to run. Add statements first.")
				continue
			}

			output, execErr := executeCode(strings.Join(codeLines, "\n"), args)

			fmt.Println(infoColor("--- Output ---"))
			fmt.Print(outputColor(output))
			fmt.Println(infoColor("--------------"))

			if execErr != nil {
				fmt.Fprintln(os.Stderr, errorColor("Code Execution Finished with Error Status."))
			} else {
				fmt.Println(successColor("Code Execution Successful."))
			}

			updatePrompt(rl)
			continue
		case ":sys":
			cmdErr, reinitializeReadline := handleSys(args, rl)
			if cmdErr != nil {
				fmt.Fprintln(os.Stderr, errorColor("Error executing system command: %v", cmdErr))
			}
			if reinitializeReadline {
				rl.Close()
				rl, err = readline.NewEx(rlConfig)
				if err != nil {
					panic(err) // If readline fails to reinitialize, the REPL cannot continue.
				}
				// After re-initializing, clean and refresh the readline instance to ensure the prompt is displayed correctly.
				rl.Clean()
				updatePrompt(rl)
				rl.Refresh()
			}
			updatePrompt(rl)
			continue
		default:
			// --- Accumulate Code ---
			codeLines = append(codeLines, input) // Use raw input to preserve indentation
			bufferDirty = true
			rl.SetPrompt(" -> ") // Change prompt for multi-line/subsequent input
		}
	}
}
