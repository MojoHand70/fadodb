package bench

import (
	"path/filepath"
	"testing"
)

// The target must configure the connection as it says: WAL mode and the
// synchronous setting, so that the campaign rows mean what they claim.
func TestSQLite_Pragmas(t *testing.T) {
	for _, tc := range []struct {
		target SQLite
		want   int64
	}{{SQLite{}, 1}, {SQLite{Synchronous: "NORMAL"}, 1}, {SQLite{Synchronous: "FULL"}, 2}, {SQLite{BackgroundCheckpoints: true}, 1}} {
		path := filepath.Join(t.TempDir(), "p.db")
		db, err := tc.target.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var mode string
		var sync, auto int64
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&auto); err != nil {
			t.Fatal(err)
		}
		db.Close()
		wantAuto := int64(1000)
		if tc.target.BackgroundCheckpoints {
			wantAuto = 0
		}
		if mode != "wal" || sync != tc.want || auto != wantAuto {
			t.Errorf("%s: journal_mode %q, synchronous %d, wal_autocheckpoint %d; want wal, %d, %d", tc.target.Name(), mode, sync, auto, tc.want, wantAuto)
		}
	}
}
