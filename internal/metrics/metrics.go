/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package metrics exposes OpenTelemetry metrics for Pacto contract compliance,
// bridged to Prometheus via the OTel Prometheus exporter registered with
// controller-runtime's metrics registry.
package metrics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/validator"
)

const meterName = "pacto.trianalab.io/operator"

var (
	complianceStatus otelmetric.Int64Gauge
	validationErrors otelmetric.Int64Gauge
	validationWarns  otelmetric.Int64Gauge
	validationResult otelmetric.Int64Gauge
)

func init() {
	// Create a Prometheus exporter that writes to controller-runtime's registry.
	exporter, err := otelprom.New(
		otelprom.WithRegisterer(ctrlmetrics.Registry),
	)
	if err != nil {
		panic("failed to create OTel Prometheus exporter: " + err.Error())
	}

	// Create a MeterProvider with the Prometheus exporter.
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	otel.SetMeterProvider(provider)

	meter := provider.Meter(meterName)

	complianceStatus, err = meter.Int64Gauge("pacto_contract_compliance_status",
		otelmetric.WithDescription("Whether the service is fully compliant with its contract (1=compliant, 0=non-compliant)"),
	)
	if err != nil {
		panic("failed to create compliance_status gauge: " + err.Error())
	}

	validationErrors, err = meter.Int64Gauge("pacto_contract_validation_errors",
		otelmetric.WithDescription("Number of error-level contract validation failures"),
	)
	if err != nil {
		panic("failed to create validation_errors gauge: " + err.Error())
	}

	validationWarns, err = meter.Int64Gauge("pacto_contract_validation_warnings",
		otelmetric.WithDescription("Number of warning-level contract validation mismatches"),
	)
	if err != nil {
		panic("failed to create validation_warnings gauge: " + err.Error())
	}

	validationResult, err = meter.Int64Gauge("pacto_contract_validation_result",
		otelmetric.WithDescription("Result of each contract validation check (1=pass, 0=fail)"),
	)
	if err != nil {
		panic("failed to create validation_result gauge: " + err.Error())
	}
}

// RecordValidation updates all metrics for a Pacto CR based on validation checks.
func RecordValidation(namespace, service string, checks []validator.Check) {
	ctx := context.Background()
	baseAttrs := []attribute.KeyValue{
		attribute.String("service", service),
		attribute.String("namespace", namespace),
	}

	var errors, warnings int64
	allPassed := true

	for _, check := range checks {
		val := int64(1)
		if !check.Passed {
			val = 0
			allPassed = false
			severity := check.Severity
			if severity == "" {
				severity = pactov1alpha1.SeverityError
			}
			if severity == pactov1alpha1.SeverityError {
				errors++
			} else {
				warnings++
			}
		}
		checkAttrs := make([]attribute.KeyValue, len(baseAttrs)+1)
		copy(checkAttrs, baseAttrs)
		checkAttrs[len(baseAttrs)] = attribute.String("check", check.Name)
		validationResult.Record(ctx, val, otelmetric.WithAttributes(checkAttrs...))
	}

	if allPassed {
		complianceStatus.Record(ctx, 1, otelmetric.WithAttributes(baseAttrs...))
	} else {
		complianceStatus.Record(ctx, 0, otelmetric.WithAttributes(baseAttrs...))
	}

	validationErrors.Record(ctx, errors, otelmetric.WithAttributes(baseAttrs...))
	validationWarns.Record(ctx, warnings, otelmetric.WithAttributes(baseAttrs...))
}
