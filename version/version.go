package version

import "fmt"

var (
	// Major version number
	Major = "0"
	// Minor version number (will be injected by the build process)
	Minor = "dev"
	// Commit hash (will be injected by the build process)
	Commit = "unknown"
)

// String returns the formatted version string.
func String() string {
	return fmt.Sprintf("%s.%s-%s", Major, Minor, Commit)
}
