package outbound

import (
	"testing"

	"github.com/MojoHand70/fadodb/internal/leak"
)

// A test that leaves a database open fails on Windows; this fails it
// here too.
func TestMain(m *testing.M) { leak.Main(m) }
