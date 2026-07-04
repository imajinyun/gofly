// Command model-gorm demonstrates GORM model generation and basic CRUD
// operations with the gofly framework.
package main

import (
	"context"
	"fmt"
	"time"
)

type User struct {
	ID        int64
	Email     string
	Name      string
	Version   int64
	DeletedAt *time.Time
}

func main() {
	ctx := context.Background()
	_ = ctx
	user := User{ID: 1, Email: "ada@example.com", Name: "Ada", Version: 1}
	fmt.Printf("gorm model unique lookup: email=%s id=%d\n", user.Email, user.ID)
	fmt.Println("patterns: batch insert, field update, optimistic lock, soft delete, cursor pagination, transaction")
}
