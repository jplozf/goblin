# Goblin - a simple Go REPL

Goblin is a simple REPL for writing and testing small snippets in Go language.

```
------ Goblin REPL Wrapper (v0.10-ce7afb1) ------
>  Enter Go statements and  :run  to  execute.  <
>  Use 'fmt.Println(...)' to display  results.  <
>  Use ':help' to see the available  commands.  <
-------------------------------------------------
go> :help

------ Goblin REPL Commands (v0.9-0b1b2e0)
:run                  - Execute the current Go code in the buffer.
:clear                - Clear the current code buffer.
:show                 - Display the current content of the code buffer.
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
--------------------------------------------------------------------------------------------
go> import "math"
 -> fmt.Println(math.Pi)
 -> :show

--- Current Code Buffer ---
   1: import "math"
   2: fmt.Println(math.Pi)
---------------------------

go> :run
--- Output ---
3.141592653589793
--------------
Code Execution Successful.
go>
```

## License

This project is licensed under the GNU General Public License - see the [LICENSE.md](LICENSE.md) file for details.
