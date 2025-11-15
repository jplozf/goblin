package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"strconv"

	"regexp"

	"github.com/chzyer/readline"

	"goblin.go/version"
)

// REPL_SAVES_DIR is the directory where code snippets will be saved and loaded from.
var REPL_SAVES_DIR = filepath.Join(os.Getenv("HOME"), ".goblin", "snippets")

// HISTORY_FILE is the path to the command history file.
var HISTORY_FILE = filepath.Join(os.Getenv("HOME"), ".goblin", "history")

// lastLoadedFilePath stores the path of the last file loaded using :load.
var lastLoadedFilePath string

// currentSnippetName stores the name of the currently active snippet (without extension).
var currentSnippetName string

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
			braceCount += strings.Count(line, "{")
			braceCount -= strings.Count(line, "}")
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
func executeCode(code string) (string, error) {
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
		if lastLoadedFilePath != "" {
			filename = ensureGoExtension(filepath.Base(lastLoadedFilePath))
			fmt.Printf("No filename provided. Saving to last loaded file: '%s'\n", filename)
		} else {
			// Generate a random filename based on timestamp
			filename = fmt.Sprintf("snippet_%s.go", time.Now().Format("20060102_150405"))
			fmt.Printf("No filename provided and no previous file loaded. Saving to new file: '%s'\n", filename)
		}
		filename = args[0]
	} else {
		fmt.Println("Usage: :save [<filename>]")
		return
	}

	// 1. Write the code to the file
	filePath := filepath.Join(REPL_SAVES_DIR, filename)
	if err := ioutil.WriteFile(filePath, []byte(code), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving code to '%s': %v\n", filename, err)
		return
	}

	currentSnippetName = strings.TrimSuffix(filepath.Base(filePath), ".go")
	fmt.Printf("Code successfully saved to '%s'.\n", filePath)
}

// handleLoad loads the specified filename into the current code buffer.
func handleLoad(codeLines *[]string, args []string) {
	if len(args) != 1 {
		fmt.Println("Usage: :load <filename>")
		return
	}
	filename := ensureGoExtension(args[0])
	filePath := filepath.Join(REPL_SAVES_DIR, filename)

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading file '%s': %v\n", filename, err)
		return
	}

	// Clear and set the new content
	*codeLines = strings.Split(string(data), "\n")

	lastLoadedFilePath = filePath // Store the last loaded file path
	currentSnippetName = strings.TrimSuffix(filepath.Base(filePath), ".go")

	fmt.Printf("Code successfully loaded from '%s'. Buffer reset and updated.\n", filePath)
}

// handleExport exports the current code buffer to a full Go source file.
func handleExport(code string, args []string) {
	outputPath := ""

	if len(args) == 0 {
		filename := ""
		if lastLoadedFilePath != "" {
			filename = ensureGoExtension(filepath.Base(lastLoadedFilePath))
			fmt.Printf("No filename provided. Exporting to last loaded file name: '%s' in home directory.\n", filename)
		} else {
			filename = fmt.Sprintf("snippet_%s.go", time.Now().Format("20060102_150405"))
			fmt.Printf("No filename provided and no previous file loaded. Exporting to new file: '%s' in home directory.\n", filename)
		}
		outputPath = filepath.Join(os.Getenv("HOME"), filename)
	} else if len(args) == 1 {
		outputPath = ensureGoExtension(args[0])
	} else {
		fmt.Println("Usage: :export [<filepath>]")
		return
	}

	// Separate code parts
	userImports, topLevelDeclarations, statements := separateCodeParts(code)

	// Fill the template with the separated code
	fullCode := fmt.Sprintf(codeTemplate, userImports, topLevelDeclarations, statements)

	// Ensure the directory exists
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory '%s': %v\n", dir, err)
		return
	}

	// Write the code to the file
	if err := ioutil.WriteFile(outputPath, []byte(fullCode), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error exporting code to '%s': %v\n", outputPath, err)
		return
	}

	fmt.Printf("Code successfully exported to '%s'.\n", outputPath)
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

	fmt.Printf("\n------ Goblin REPL Commands (v%s)\n", version.String())
	fmt.Println(":run                  - Execute the current Go code in the buffer.")
	fmt.Println(":clear                - Clear the current code buffer.")
	fmt.Println(":show                 - Display the current content of the code buffer.")
	fmt.Println(":list                 - List all saved code snippets.")
	fmt.Println(":save <file>          - Save the current code buffer to a file.")
	fmt.Println(":load <file>          - Load code from a file into the buffer, replacing current content.")
	fmt.Println(":export <filepath>    - Export the current code buffer to a full Go source file.")
	fmt.Println(":edit                 - Open the current code buffer in an external editor for modification.")
	fmt.Println(":u(ndo)               - Remove the last entry from the buffer.")
	fmt.Println(":d(elete) <line>      - Delete a specific line from the buffer by its number.")
	fmt.Println(":help                 - Display this help message.")
	fmt.Println(":q(uit), :exit, :bye  - Exit the REPL.")
	fmt.Println("--------------------------------------------------------------------------------------------")

}

func updatePrompt(rl *readline.Instance) {
	if currentSnippetName != "" {
		rl.SetPrompt(fmt.Sprintf("[%s]go> ", currentSnippetName))
	} else {
		rl.SetPrompt("go> ")
	}
}

func main() {
	initConfig() // Ensure ~/.goblin exists

	fmt.Printf("------ Goblin REPL Wrapper (v%s) ------\n", version.String())
	fmt.Println("> Enter Go statements and :run to execute.")
	fmt.Println("> Use 'fmt.Println(...)' to display results.")
	fmt.Println("> Use ':help' to see the available commands.")
	fmt.Println("------------------------------------------------")

	var codeLines []string
	currentSnippetName = ""

	rl, err := readline.NewEx(&readline.Config{
		Prompt:      "go> ",
		HistoryFile: HISTORY_FILE,
	})
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	updatePrompt(rl)

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
		case ":quit", ":exit", ":bye", ":q":
			fmt.Println("Exiting Goblin REPL.")
			return
		case ":clear":
			codeLines = []string{}
			currentSnippetName = ""
			fmt.Println("Code buffer cleared.")
			updatePrompt(rl)
			continue
		case ":show":
			if len(codeLines) == 0 {
				fmt.Println("Code buffer is empty.")
			} else {
				fmt.Println("\n--- Current Code Buffer ---")
				for i, line := range codeLines {
					fmt.Printf("%4d: %s\n", i+1, line)
				}
				fmt.Println("---------------------------\n")
			}
			updatePrompt(rl)
			continue
		case ":list":
			handleList()
			updatePrompt(rl)
			continue
		case ":save":
			handleSave(strings.Join(codeLines, "\n"), args)
			updatePrompt(rl)
			continue
		case ":load":
			handleLoad(&codeLines, args)
			updatePrompt(rl)
			continue
		case ":export":
			if len(codeLines) == 0 {
				fmt.Println("No code in buffer to export.")
				continue
			}
			handleExport(strings.Join(codeLines, "\n"), args)
			updatePrompt(rl)
			continue
		case ":edit":
			handleEdit(&codeLines)
			updatePrompt(rl)
			continue
		case ":delete", ":d":
			if len(args) != 1 {
				fmt.Println("Usage: :delete <line_number>")
				continue
			}
			lineNum, err := strconv.Atoi(args[0])
			if err != nil || lineNum < 1 || lineNum > len(codeLines) {
				fmt.Printf("Invalid line number: %s. Please provide a number between 1 and %d.\n", args[0], len(codeLines))
				continue
			}
			// Adjust for 0-based indexing
			indexToDelete := lineNum - 1
			codeLines = append(codeLines[:indexToDelete], codeLines[indexToDelete+1:]...)
			fmt.Printf("Line %d deleted. Current buffer:\n", lineNum)
			// Re-display the buffer with line numbers
			// This re-uses the logic from the :show command
			if len(codeLines) == 0 {
				fmt.Println("Code buffer is empty.")
			} else {
				fmt.Println("\n--- Current Code Buffer ---")
				for i, line := range codeLines {
					fmt.Printf("%4d: %s\n", i+1, line)
				}
				fmt.Println("---------------------------\n")
			}
			updatePrompt(rl)
			continue
		case ":help":
			handleHelp()
			updatePrompt(rl)
			continue
		case ":undo", ":u":
			if len(codeLines) > 0 {
				codeLines = codeLines[:len(codeLines)-1]
				fmt.Println("Last entry removed.")
			} else {
				fmt.Println("Buffer is empty, nothing to undo.")
			}
			updatePrompt(rl)
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

			updatePrompt(rl)
			continue
		default:
			// --- Accumulate Code ---
			codeLines = append(codeLines, line)
			rl.SetPrompt("  -> ") // Change prompt for multi-line/subsequent input
		}
	}
}
