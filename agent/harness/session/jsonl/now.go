package jsonl

import "time"

func defaultNow() float64 { return float64(time.Now().UnixMilli()) }
