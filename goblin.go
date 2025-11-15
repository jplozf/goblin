package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"regexp"

	"github.com/chzyer/readline"

	"goblin.go/version"
)

// REPL_SAVES_DIR is the directory where code snippets will be saved and loaded from.
var REPL_SAVES_DIR = filepath.Join(os.Getenv("HOME"), ".goblin", "snippets")

// HISTORY_FILE is the path to the command history file.
var HISTORY_FILE = filepath.Join(os.Getenv("HOME"), ".goblin", "history")

// initConfig ensures the configuration directory and necessary subdirectories exist.
func initConfig() {
	// Create the main ~/.goblin directory
	if err := os.MkdirAll(filepath.Dir(REPL_SAVES_DIR), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}
	// Create the snippets subdirectory
	if err := os.MkdirAll(REPL_SAVES_DIR, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating snippets directory: %v\n", err)
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

// executeCode takes the accumulated user code, separates declarations from statements,
// wraps them in the template, writes to a temporary file, and executes it.
func executeCode(code string) (string, error) {
	var userImports, topLevelDeclarations, statements strings.Builder
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
			continue // Do not write "import (" to userImports
		}
		if inImportBlock {
			braceCount += strings.Count(line, "{")
			braceCount -= strings.Count(line, "}")
			if braceCount <= 0 { // End of import block
				inImportBlock = false
				braceCount = 0 // Reset brace count
				continue // Do not write ")" to userImports
			}
			// This is an import path within a group
			userImports.WriteString(line + "\n")
			continue
		}
		if matches := importSingleRegex.FindStringSubmatch(trimmedLine); len(matches) > 1 {
			// This is a single-line import, extract the path and format it
			userImports.WriteString("\t" + matches[1] + "\n")
			continue
		}

		// --- Handle Global Declarations (var, const, type) ---
		if !inFuncDecl && !inImportBlock && globalDeclStartRegex.MatchString(trimmedLine) {
			// Check for multi-line var/const/type blocks
			if strings.HasSuffix(trimmedLine, "(") { // e.g., var (
				inGlobalDeclBlock = true
				topLevelDeclarations.WriteString(line + "\n")
				braceCount += strings.Count(line, "(")
				braceCount -= strings.Count(line, ")")
				continue
			} else { // Single line var/const/type
				topLevelDeclarations.WriteString(line + "\n")
				continue
			}
		}
		if inGlobalDeclBlock {
			topLevelDeclarations.WriteString(line + "\n")
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
			topLevelDeclarations.WriteString(line + "\n")
			braceCount += strings.Count(line, "{")
			braceCount -= strings.Count(line, "}")
			continue
		}
		if inFuncDecl {
			topLevelDeclarations.WriteString(line + "\n")
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
			statements.WriteString(line + "\n")
		}
	}

	// 1. Fill the template with the separated code
	fullCode := fmt.Sprintf(codeTemplate, userImports.String(), topLevelDeclarations.String(), statements.String())

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
			fmt.Printf("> %s (%d bytes)\n", file.Name(), file.Size())
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

	// 1. Write the code to the file
	filePath := filepath.Join(REPL_SAVES_DIR, filename)
	if err := ioutil.WriteFile(filePath, []byte(code), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving code to '%s': %v\n", filename, err)
		return
	}

	fmt.Printf("Code successfully saved to '%s'.\n", filePath)
}

// handleLoad loads the specified filename into the current code buffer.
func handleLoad(codeLines *[]string, args []string) {
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
	*codeLines = strings.Split(string(data), "\n")

	fmt.Printf("Code successfully loaded from '%s'. Buffer reset and updated.\n", filePath)
}

// handleEdit opens the current code buffer in an external editor.
func handleEdit(codeLines *[]string) {
	// 1. Create a temporary file with a .go extension
	tmpfile, err := ioutil.TempFile("", "goblin-*.go")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temporary file: %v\n", err)
		return
	}
	defer os.Remove(tmpfile.Name()) // Clean up the file afterwards

	// 2. Write the current buffer to the temporary file
	if _, err := tmpfile.WriteString(strings.Join(*codeLines, "\n")); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to temporary file: %v\n", err)
		return
	}
	if err := tmpfile.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Error closing temporary file: %v\n", err)
		return
	}

	// 3. Get the user's preferred editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		// Fallback to a common default if EDITOR is not set
		editor = "vim"
	}

	// 4. Open the file in the editor
	cmd := exec.Command(editor, tmpfile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error opening editor '%s': %v\n", editor, err)
		return
	}

	// 5. Read the modified content back into the buffer
	data, err := ioutil.ReadFile(tmpfile.Name())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading modified file: %v\n", err)
		return
	}

	// 6. Reset the buffer and write the new content
	*codeLines = strings.Split(string(data), "\n")

	fmt.Println("Buffer updated from editor.")

}

// handleHelp displays a list of available commands.

func handleHelp() {

	fmt.Println("\n--- Goblin REPL Commands ---")
	fmt.Println(":run         - Execute the current Go code in the buffer.")
	fmt.Println(":clear       - Clear the current code buffer.")
	fmt.Println(":show        - Display the current content of the code buffer.")
	fmt.Println(":list        - List all saved code snippets.")
	fmt.Println(":save <file> - Save the current code buffer to a file.")
	fmt.Println(":load <file> - Load code from a file into the buffer, replacing current content.")
	fmt.Println(":edit        - Open the current code buffer in an external editor for modification.")
	fmt.Println(":undo, :u    - Remove the last entry from the buffer.")
	fmt.Println(":help        - Display this help message.")
	fmt.Println(":quit, :exit - Exit the REPL.")
	fmt.Println("----------------------------")

}

func main() {
	initConfig() // Ensure ~/.goblin exists

	fmt.Printf("--- Go REPL Wrapper (v%s) ---\n", version.String())
	fmt.Println("Enter Go statements (must be valid inside func main()).")
	fmt.Println("Use 'fmt.Println(...)' to display results.")
	fmt.Println("Use ':help' to see the available commands.")
	fmt.Println("--------------------------------------")

	var codeLines []string

	rl, err := readline.NewEx(&readline.Config{
		Prompt:      "go> ",
		HistoryFile: HISTORY_FILE,
	})
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	for {
		// Read line input
		input, err := rl.Readline()
		if err != nil { // io.EOF, readline.ErrInterrupt
			fmt.Println("\nExiting Goblin REPL.")
			break
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
		case ":quit", ":exit", ":bye":
			fmt.Println("Exiting Go REPL.")
			return
		case ":clear":
			codeLines = []string{}
			fmt.Println("Code buffer cleared.")
			rl.SetPrompt("go> ")
			continue
		case ":show":
			if len(codeLines) == 0 {
				fmt.Println("Code buffer is empty.")
			} else {
				fmt.Println("\n--- Current Code Buffer ---\n" + strings.Join(codeLines, "\n") + "\n---------------------------\n")
			}
			continue
		case ":list":
			handleList()
			rl.SetPrompt("go> ")
			continue
		case ":save":
			handleSave(strings.Join(codeLines, "\n"), args)
			rl.SetPrompt("go> ")
			continue
		case ":load":
			handleLoad(&codeLines, args)
			rl.SetPrompt("go> ")
			continue
		case ":edit":
			handleEdit(&codeLines)
			rl.SetPrompt("go> ")
			continue
		case ":help":
			handleHelp()
			rl.SetPrompt("go> ")
			continue
		case ":undo", ":u":
			if len(codeLines) > 0 {
				codeLines = codeLines[:len(codeLines)-1]
				fmt.Println("Last entry removed.")
			} else {
				fmt.Println("Buffer is empty, nothing to undo.")
			}
			continue
		case ":run":
			// Execute the accumulated code
			if len(codeLines) == 0 {
				fmt.Println("No code to run. Add statements first.")
				continue
			}

			output, execErr := executeCode(strings.Join(codeLines, "\n"))

			fmt.Println("--- Output ---")
			fmt.Print(output)
			fmt.Println("--------------")

			if execErr != nil {
				fmt.Fprintf(os.Stderr, "Code Execution Finished with Error Status.\n")
			} else {
				fmt.Println("Code Execution Successful.")
			}

			rl.SetPrompt("go> ")
			continue
		default:
			// --- Accumulate Code ---
			codeLines = append(codeLines, line)
			rl.SetPrompt("  -> ") // Change prompt for multi-line/subsequent input
		}
	}
}
