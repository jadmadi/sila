package contract

import (
	"fmt"
	"strings"
)

// aggregate turns individual checks into a policy-aware decision.
func aggregate(base Decision, policy EffectivePolicy, checks map[string]CheckResult, code, reason, recovery string) Decision {
	base.Checks = checks
	if code != "" {
		base.ReasonCode = code
		base.Reason = reason
		base.Recovery = recovery
	}
	failCode, failReason, failRecovery := "", "", ""
	warnCode, warnReason, warnRecovery := "", "", ""
	fails, warns := 0, 0
	for _, name := range checkOrder {
		cr, ok := checks[name]
		if !ok {
			continue
		}
		switch cr.Status {
		case CheckFail:
			fails++
			if failCode == "" {
				failCode = cr.Code
				if failCode == "" {
					failCode = checkReason[name]
				}
				failReason = name + ": " + cr.Detail
				failRecovery = cr.Recovery
			}
		case CheckWarn:
			warns++
			if warnCode == "" {
				warnCode = cr.Code
				if warnCode == "" {
					warnCode = "CONTRACT_WARN"
				}
				warnReason = name + ": " + cr.Detail
				warnRecovery = cr.Recovery
			}
		}
	}
	if code != "" && fails == 0 && warns == 0 {
		switch policy.Policy {
		case PolicyEnforce:
			base.Decision = DecisionDeny
		case PolicyWarn:
			base.Decision = DecisionWarn
		default:
			base.Decision = DecisionAllow
		}
		return base
	}
	switch policy.Policy {
	case PolicyEnforce:
		if fails > 0 {
			base.Decision = DecisionDeny
			base.ReasonCode = failCode
			base.Reason = failReason
			base.Recovery = failRecovery
			return base
		}
		if warns > 0 {
			base.Decision = DecisionWarn
			base.ReasonCode = warnCode
			base.Reason = warnReason
			base.Recovery = warnRecovery
			return base
		}
	case PolicyWarn:
		if fails > 0 || warns > 0 {
			base.Decision = DecisionWarn
			if fails > 0 {
				base.ReasonCode = failCode
				base.Reason = failReason
				base.Recovery = failRecovery
			} else {
				base.ReasonCode = warnCode
				base.Reason = warnReason
				base.Recovery = warnRecovery
			}
			return base
		}
	case PolicyOff:
		base.Decision = DecisionAllow
		base.ReasonCode = ReasonPolicyOff
		return base
	}
	base.Decision = DecisionAllow
	return base
}

// Summary renders a one-line decision for humans.
func Summary(d Decision) string {
	if d.Decision == DecisionAllow {
		if d.Bypassed {
			return "allowed (bypassed: " + d.ReasonCode + ")"
		}
		return "allowed"
	}
	parts := []string{d.Decision}
	if d.ReasonCode != "" {
		parts = append(parts, d.ReasonCode)
	}
	if d.Reason != "" {
		parts = append(parts, d.Reason)
	}
	return strings.Join(parts, ": ")
}

// FailedChecks returns the failing check names in deterministic order.
func FailedChecks(d Decision) []string {
	var out []string
	for _, name := range checkOrder {
		if cr, ok := d.Checks[name]; ok && cr.Status == CheckFail {
			out = append(out, fmt.Sprintf("%s (%s)", name, cr.Detail))
		}
	}
	return out
}
