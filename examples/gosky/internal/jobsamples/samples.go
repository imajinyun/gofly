// Package jobsamples contains code-owned demo executors for the
// support/job console, equivalent to SmartJobSample1/2 in the Java
// reference. They perform no side effects and return a bounded result so
// the scheduler pipeline can be demonstrated from the unmodified UI.
package jobsamples

import (
	"context"
	"fmt"
	"time"
)

// Sample1Key and Sample2Key are the code-owned executor keys that must be
// entered into the jobClass field of the support/job console.
const (
	Sample1Key = "gosky.support.job.SampleJob1"
	Sample2Key = "gosky.support.job.SampleJob2"
)

// SampleJob1 returns a timestamped acknowledgement.
type SampleJob1 struct{}

// Execute implements jobruntime.Job.
func (SampleJob1) Execute(_ context.Context, param string) (string, error) {
	return fmt.Sprintf("sample job 1 executed at %s param=%q",
		time.Now().Format(time.RFC3339), param), nil
}

// SampleJob2 returns the configured parameter.
type SampleJob2 struct{}

// Execute implements jobruntime.Job.
func (SampleJob2) Execute(_ context.Context, param string) (string, error) {
	return fmt.Sprintf("sample job 2 executed with param=%q", param), nil
}
