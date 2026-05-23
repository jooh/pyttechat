package buildinfo

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Version string
	Commit  string
	Date    string
}

func Snapshot() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}

func Summary() string {
	info := Snapshot()
	return fmt.Sprintf("version=%s commit=%s date=%s", info.Version, info.Commit, info.Date)
}
