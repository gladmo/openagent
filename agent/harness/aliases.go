package harness

import (
	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/telemetry"
)

type abortSignalType = abort.Signal
type telemetrySpanType = telemetry.TelemetrySpan

func spanStatusError() telemetry.SpanStatus { return telemetry.StatusErrorWithoutDetails() }
