// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package tailsamplingprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor"

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/metadata"
	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"
)

func getNewAndPolicy(settings component.TelemetrySettings, tel *metadata.TelemetryBuilder, policyName string, config *AndCfg) (sampling.PolicyEvaluator, error) {
	subPolicyEvaluators := make([]sampling.PolicyEvaluator, len(config.SubPolicyCfg))
	for i := range config.SubPolicyCfg {
		policyCfg := &config.SubPolicyCfg[i]
		//policy, err := getAndSubPolicyEvaluator(settings, policyCfg)
		policy, err := getPolicyEvaluator(settings, tel, policyCfg)
		if err != nil {
			return nil, err
		}
		subPolicyEvaluators[i] = policy
	}
	return sampling.NewAnd(settings.Logger, tel, policyName, subPolicyEvaluators), nil
}
