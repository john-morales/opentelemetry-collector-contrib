// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package tailsamplingprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor"

import (
	"fmt"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/metadata"
	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"
)

func getNewNotPolicy(settings component.TelemetrySettings, tel *metadata.TelemetryBuilder, policyName string, config *NotCfg) (sampling.PolicyEvaluator, error) {
	if len(config.SubPolicyCfg) != 0 {
		return nil, fmt.Errorf("'not_sub_policy' must contain example on entry, found %d", len(config.SubPolicyCfg))
	}
	policyCfg := &config.SubPolicyCfg[0]
	//notPolicy, err := getNotSubPolicyEvaluator(settings, policyCfg)
	notPolicy, err := getPolicyEvaluator(settings, tel, policyCfg)
	if err != nil {
		return nil, err
	}
	return sampling.NewNot(settings.Logger, tel, policyName, notPolicy), nil
}
