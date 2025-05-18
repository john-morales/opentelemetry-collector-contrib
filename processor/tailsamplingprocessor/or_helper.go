// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package tailsamplingprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor"

import (
	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"
)

func getNewOrPolicy(settings component.TelemetrySettings, config *OrCfg) (sampling.PolicyEvaluator, error) {
	subPolicyEvaluators := make([]sampling.PolicyEvaluator, len(config.SubPolicyCfg))
	for i := range config.SubPolicyCfg {
		policyCfg := &config.SubPolicyCfg[i]
		//orPolicy, err := getOrSubPolicyEvaluator(settings, policyCfg)
		orPolicy, err := getPolicyEvaluator(settings, policyCfg)
		if err != nil {
			return nil, err
		}
		subPolicyEvaluators[i] = orPolicy
	}
	return sampling.NewOr(settings.Logger, subPolicyEvaluators), nil
}

// Return instance of and sub-policy
func getOrSubPolicyEvaluator(settings component.TelemetrySettings, cfg *OrSubPolicyCfg) (sampling.PolicyEvaluator, error) {
	return getSharedPolicyEvaluator(settings, &cfg.sharedPolicyCfg)
}
