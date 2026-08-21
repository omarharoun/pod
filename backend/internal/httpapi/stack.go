package httpapi

import "runtime"

func stack() string {
	buf := make([]byte, 8<<10)
	return string(buf[:runtime.Stack(buf, false)])
}
