# Goblin - a simple Go REPL

Goblin is a simple REPL for writing and testing small snippets in Go language.

```
🐗 Goblin 0.25-351f2b4 - An enhanced REPL for Go.
go version go1.25.4 X:nodwarf5 linux/amd64

Enter Go statements and type ':run' to execute.
Type 'fmt.Println(...)' to display results, don't forget to 'import "fmt"' first.
Type ':help' to see the available commands.

go> import "fmt"
 -> import "math"
 -> fmt.Println(math.Pi)
 -> :run
--- Output ---
3.141592653589793
--------------
Code Execution Successful.
go> :tidy
Code buffer tidied.

--- Current Code Buffer ---
   1: import (
   2:   "fmt"
   3:   "math"
   4: )
   5: 
   6: fmt.Println(math.Pi)
---------------------------
go> :help

🐗 Goblin 0.25-351f2b4 - Commands summary :
:run [args...]           - Execute the current Go code in the buffer with optional arguments.
:sys <command> [args...] - Execute a system command.
:clear                   - Clear the current code buffer.
:show                    - Display the current content of the code buffer.
:tidy                    - Format the code in the buffer.
:list                    - List all saved code snippets.
:save <file>             - Save the current code buffer to a file.
:saveas <file>           - Save the current buffer to a new file and make it the active snippet.
:load <file>             - Load code from a file into the buffer, replacing current content.
:rename <new_name>       - Rename the current snippet.
:export <filepath>       - Export the current code buffer to a full Go source file.
:edit                    - Open the current code buffer in an external editor for modification.
:u(ndo)                  - Remove the last entry from the buffer.
:d(elete) <line>         - Delete a specific line from the buffer by its number.
:i(nsert) <line>         - Insert an empty line before the provided line number.
:help                    - Display this help message.
:q(uit), :exit, :bye     - Exit the REPL.

go> :sys pwd
/media/HDD/Documents/Google Drive/Projets/Go/jplozf/goblin

go> :q
Current snippet has unsaved changes. Save now? (y/n) n

🐗 Goblin 0.25-351f2b4 - https://github.com/jplozf/goblin
```

## License

This project is licensed under the GNU General Public License - see the [LICENSE.md](LICENSE.md) file for details.
