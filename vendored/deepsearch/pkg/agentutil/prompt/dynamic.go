package prompt

import (
	"fmt"
	"time"
)

func DatePrompt() string {
	location := time.FixedZone("CST", 60*60*8)
	return fmt.Sprintf(
		"Current date in Asia/Shanghai: %s. "+
			"The exact time is omitted for KV cache efficiency. "+
			"If you need the current time, call the GetTime tool.",
		time.Now().In(location).Format("2006-01-02"),
	)
}
