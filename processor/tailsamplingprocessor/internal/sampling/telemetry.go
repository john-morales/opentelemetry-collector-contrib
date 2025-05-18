package sampling

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/metadata"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var GlobalTelemetryBuilder *metadata.TelemetryBuilder = nil

var (
	attrSampledTrue     = metric.WithAttributes(attribute.String("sampled", "true"))
	attrSampledFalse    = metric.WithAttributes(attribute.String("sampled", "false"))
	decisionToAttribute = map[Decision]metric.MeasurementOption{
		Sampled:          attrSampledTrue,
		NotSampled:       attrSampledFalse,
		InvertNotSampled: attrSampledFalse,
		InvertSampled:    attrSampledTrue,
	}
)
