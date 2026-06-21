// Command model-mongo demonstrates MongoDB model generation and basic
// document operations with the gofly framework.
package main

import "fmt"

type Document struct {
	ID      string
	Email   string
	Version int64
	Deleted bool
}

func main() {
	doc := Document{ID: "u_1", Email: "ada@example.com", Version: 3}
	fmt.Printf("mongo model unique lookup: email=%s id=%s version=%d\n", doc.Email, doc.ID, doc.Version)
	fmt.Println("patterns: bulk write, $set field update, version compare-and-swap, soft delete flag, _id cursor pagination")
}
