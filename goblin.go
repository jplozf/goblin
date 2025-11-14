package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// REPL_SAVES_DIR is the directory where code snippets will be saved and loaded from.
const REPL_SAVES_DIR = "go_repl_saves"

// codeTemplate provides the minimal Go program structure required to run code.
const codeTemplate = `
package main

import (
	// Only fmt is included by default to prevent unused import errors.
	// Add other necessary imports (e.g., "math", "time") in your REPL input if needed.
	"fmt"
)

// Global variables or helper functions can be declared here if needed,
// but for this simple REPL, user code is executed inside main.

func main() {
	// Start of user code execution block
%s
	// End of user code execution block
}
`

// executeCode takes the accumulated user code, wraps it in the template,
// writes it to a temporary file, and executes it using 'go run'.
func executeCode(code string) (string, error) {
	// 1. Fill the template with user code
	fullCode := fmt.Sprintf(codeTemplate, code)

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
	cmd := exec.Command("go", "run", tmpFilePath)

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
	fmt.Println("--- Saved Snippets ---")
	files, err := ioutil.ReadDir(REPL_SAVES_DIR)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Save directory '%s' does not exist yet.\n", REPL_SAVES_DIR)
			return
		}
		fmt.Fprintf(os.Stderr, "Error listing files: %v\n", err)
		return
	}

	if len(files) == 0 {
		fmt.Println("No saved files found.")
		return
	}

	for _, file := range files {
		if !file.IsDir() {
			fmt.Printf("- %s (%d bytes)\n", file.Name(), file.Size())
		}
	}
	fmt.Println("----------------------")
}

// handleSave saves the current code buffer to the specified filename.
func handleSave(code string, args []string) {
	if len(args) != 1 {
		fmt.Println("Usage: :save <filename>")
		return
	}
	filename := args[0]

	// 1. Ensure the save directory exists
	if err := os.MkdirAll(REPL_SAVES_DIR, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating save directory: %v\n", err)
		return
	}

	// 2. Write the code to the file
	filePath := filepath.Join(REPL_SAVES_DIR, filename)
	if err := ioutil.WriteFile(filePath, []byte(code), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving code to '%s': %v\n", filename, err)
		return
	}

	fmt.Printf("Code successfully saved to '%s'.\n", filePath)
}

// handleLoad loads the specified filename into the current code buffer.
func handleLoad(currentCode *strings.Builder, args []string) {
	if len(args) != 1 {
		fmt.Println("Usage: :load <filename>")
		return
	}
	filename := args[0]
	filePath := filepath.Join(REPL_SAVES_DIR, filename)

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading file '%s': %v\n", filename, err)
		return
	}

	// Clear and set the new content
	currentCode.Reset()
	currentCode.WriteString(string(data))

	fmt.Printf("Code successfully loaded from '%s'. Buffer reset and updated.\n", filePath)
}


func main() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("--- Go REPL Wrapper (v1.1) ---")
	fmt.Println("Enter Go statements (must be valid inside func main()).")
	fmt.Println("Use 'fmt.Println(...)' to display results.")
	fmt.Println("Commands: ':run', ':clear', ':show', ':quit', ':save <file>', ':load <file>', ':list'")
	fmt.Println("------------------------------")

	var currentCode strings.Builder
	prompt := "go> "

	for {
		fmt.Print(prompt)
		// Read line input
		input, err := reader.ReadString('\n')
		if err != nil {
			// Handle EOF (Ctrl+D) gracefully
			if err.Error() == "EOF" {
				fmt.Println("\nExiting REPL.")
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			continue
		}

		line := strings.TrimSpace(input)
		fields := strings.Fields(line) // Split input into command and arguments

		if len(fields) == 0 {
			continue // Skip empty line
		}

		cmd := fields[0]
		args := fields[1:]

		// --- Handle REPL Commands ---
		switch cmd {
		case ":quit", ":exit":
			fmt.Println("Exiting Go REPL.")
			return
		case ":clear":
			currentCode.Reset()
			fmt.Println("Code buffer cleared.")
			prompt = "go> "
			continue
		case ":show":
			if currentCode.Len() == 0 {
				fmt.Println("Code buffer is empty.")
			} else {
				fmt.Println("\n--- Current Code Buffer ---\n" + currentCode.String() + "---------------------------\n")
			}
			continue
		case ":list":
			handleList()
			prompt = "go> "
			continue
		case ":save":
			handleSave(currentCode.String(), args)
			prompt = "go> "
			continue
		case ":load":
			handleLoad(&currentCode, args)
			prompt = "go> "
			continue
		case ":run":
			// Execute the accumulated code
			if currentCode.Len() == 0 {
				fmt.Println("No code to run. Add statements first.")
				continue
			}

			output, execErr := executeCode(currentCode.String())

			fmt.Println("--- Output ---")
			fmt.Print(output)
			fmt.Println("--------------")

			if execErr != nil {
				fmt.Fprintf(os.Stderr, "Code Execution Finished with Error Status.\n")
			} else {
				fmt.Println("Code Execution Successful.")
			}

			prompt = "go> "
			continue
		default:
			// --- Accumulate Code ---
			currentCode.WriteString(line)
			currentCode.WriteString("\n")
			prompt = "  -> " // Change prompt for multi-line/subsequent input
		}
	}
}