package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/doctor"
)

func TestIndependentCommandDependencies(t *testing.T) {
	for i := range 16 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			r := newRunner()
			marker := fmt.Sprintf("invocation-%d", i)
			r.buildDoctorReport = func(context.Context, config.Config, error) doctor.Report {
				return doctor.Report{Checks: []doctor.Check{{Name: marker}}}
			}
			var stdout, stderr bytes.Buffer
			if code := r.RunContext(context.Background(), []string{"doctor"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), marker) {
				t.Fatalf("dependency escaped invocation: %s", stdout.String())
			}
		})
	}
}
