package infra

// Version and Commit are set at build time via -ldflags.
// Example: go build -ldflags "-X github.com/jackstrohm/jot/internal/infra.Version=v1.2.4 -X github.com/jackstrohm/jot/internal/infra.Commit=a1b2c3d"
var (
	Version = "dev"
	Commit  = "none"
)
