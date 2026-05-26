// Tiny diagnostic that dumps key tables from library.db so we can see what
// is actually persisted vs. what the HTTP layer is serving. Not shipped.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: dumpdb <data_dir>")
		os.Exit(2)
	}
	dbPath := os.Args[1] + "/library.db"
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	fmt.Println("=== books ===")
	rows, _ := db.Query(`SELECT id, title, encoding, char_count, chapter_count FROM books`)
	for rows.Next() {
		var id, cc, chc int64
		var title, enc string
		_ = rows.Scan(&id, &title, &enc, &cc, &chc)
		fmt.Printf("id=%d title=%q enc=%s chars=%d chaps=%d valid_utf8=%v\n",
			id, title, enc, cc, chc, utf8.ValidString(title))
	}
	rows.Close()

	fmt.Println("\n=== chapters ===")
	rows, _ = db.Query(`SELECT book_id, idx, title, char_offset FROM chapters ORDER BY book_id, idx`)
	for rows.Next() {
		var bid, idx, off int64
		var title string
		_ = rows.Scan(&bid, &idx, &title, &off)
		fmt.Printf("book=%d idx=%d title=%q off=%d valid_utf8=%v\n",
			bid, idx, title, off, utf8.ValidString(title))
	}
	rows.Close()
}
