package privsep

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"syscall"

	_ "modernc.org/sqlite"
)

func openJournal(path string) (*sql.DB, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	f.Close()
	if err != nil {
		return nil, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || fi.Mode().Perm()&0077 != 0 || st.Nlink != 1 {
		return nil, fmt.Errorf("journal must be a private service-owned regular file")
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`PRAGMA busy_timeout=5000`,
		`PRAGMA synchronous=FULL`,
		`CREATE TABLE IF NOT EXISTS executor_jobs(id TEXT PRIMARY KEY,digest TEXT NOT NULL,status TEXT NOT NULL,result TEXT NOT NULL,error TEXT NOT NULL)`,
		`UPDATE executor_jobs SET status='interrupted',result='{"stage":"interrupted","rollbackStatus":"unknown"}',error='Executor stopped before a durable result; inspect restored runtime' WHERE status='running'`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}
