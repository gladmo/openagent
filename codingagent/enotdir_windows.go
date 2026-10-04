//go:build windows

package codingagent

import "errors"

func syscallENOTDIR() error { return errors.New("not a directory") }
