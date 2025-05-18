// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package tailsamplingprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor"

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/metadata"
	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"
)

func getNewOrPolicy(settings component.TelemetrySettings, tel *metadata.TelemetryBuilder, policyName string, config *OrCfg) (sampling.PolicyEvaluator, error) {
	subPolicyEvaluators := make([]sampling.PolicyEvaluator, len(config.SubPolicyCfg))
	for i := range config.SubPolicyCfg {
		policyCfg := &config.SubPolicyCfg[i]
		//orPolicy, err := getOrSubPolicyEvaluator(settings, policyCfg)
		orPolicy, err := getPolicyEvaluator(settings, tel, policyCfg)
		if err != nil {
			return nil, err
		}
		subPolicyEvaluators[i] = orPolicy
	}
	return sampling.NewOr(settings.Logger, tel, policyName, subPolicyEvaluators), nil
}
