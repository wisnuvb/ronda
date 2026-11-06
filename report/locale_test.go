package report

import (
	"time"

	"github.com/wisnuvb/ronda/model"
)

// jakarta is Western Indonesia Time (UTC+7, no daylight saving), the zone this package's tests are written
// in. It is a variable, not an init(), so that package-level test values built from it are initialised
// after the program's zone is set; dst_test.go covers the zones that have daylight saving.
var jakarta = func() *time.Location {
	if err := model.SetTimezone("Asia/Jakarta"); err != nil {
		panic(err)
	}
	return model.Loc()
}()
