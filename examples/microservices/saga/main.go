// Command saga demonstrates the saga pattern for distributed transactions.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/imajinyun/gofly/core/saga"
)

func main() {
	ctx := context.Background()
	var log []string
	workflow := saga.New().
		Step("reserve", func(context.Context) error { log = append(log, "reserve"); return nil }, func(context.Context) error { log = append(log, "release"); return nil }).
		Step("charge", func(context.Context) error { return errors.New("card declined") }, func(context.Context) error { log = append(log, "refund"); return nil })
	err := workflow.Execute(ctx)
	fmt.Printf("saga err=%v log=%v\n", err, log)
}
