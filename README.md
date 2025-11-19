# Goblin - a simple Go REPL

Goblin is a simple REPL for writing and testing small snippets in Go language.

```
🐗 Goblin 0.19-8f4ef34 - An enhanced REPL for Go.
go version go1.25.4 X:nodwarf5 linux/amd64
Enter Go statements and type ':run' to execute.
Type 'fmt.Println(...)' to display results.
Type ':help' to see the available commands.

go> :help

🐗 Goblin 0.19-8f4ef34 - Commands summary :
:run [args...]        - Execute the current Go code in the buffer with optional arguments.
:clear                - Clear the current code buffer.
:show                 - Display the current content of the code buffer.
:tidy                 - Format the code in the buffer.
:list                 - List all saved code snippets.
:save <file>          - Save the current code buffer to a file.
:saveas <file>        - Save the current buffer to a new file and make it the active snippet.
:load <file>          - Load code from a file into the buffer, replacing current content.
:rename <new_name>    - Rename the current snippet.
:export <filepath>    - Export the current code buffer to a full Go source file.
:edit                 - Open the current code buffer in an external editor for modification.
:u(ndo)               - Remove the last entry from the buffer.
:d(elete) <line>      - Delete a specific line from the buffer by its number.
:i(nsert) <line>      - Insert an empty line before the provided line number.
:help                 - Display this help message.
:q(uit), :exit, :bye  - Exit the REPL.

go> :list
--- Saved Snippets ---
> hello.go (72 bytes)
> nbJours.go (592 bytes)
> new01.go (36 bytes)
> new02.go (34 bytes)
> snippet_20251116_214840.go (29 bytes)
> test.go (13 bytes)
----------------------
go> :load hello
Code successfully loaded from '/home/jpl/.goblin/snippets/hello.go'. Buffer reset and updated.
[hello]go> :show

--- Current Code Buffer ---
   1: import "os"
   2: import "math"
   3: fmt.Println(len(os.Args))
   4: fmt.Println(math.Pi)
---------------------------
[hello]go> :run
--- Output ---
1
3.141592653589793
--------------
Code Execution Successful.
[hello]go> 
```

## License

This project is licensed under the GNU General Public License - see the [LICENSE.md](LICENSE.md) file for details.
