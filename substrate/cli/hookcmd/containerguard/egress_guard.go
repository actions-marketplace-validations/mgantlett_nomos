package containerguard

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

var (
	httpURLTokenRe   = regexp.MustCompile(`https?://[^\s"'` + "`" + `<>;|&()]+`)
	shellNetVerbRe   = regexp.MustCompile(`(?i)(?:^|[;&|(\s])(?:curl|wget|http|xh)\b`)
	egressSecretToks = []*regexp.Regexp{
		regexp.MustCompile(`\bghp_[A-Za-z0-9]{16,}\b`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{16,}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{12,}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`),
		regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{12,}\b`),
	}
	blockedMetadataHosts = map[string]bool{
		"169.254.169.254":          true,
		"169.254.170.2":            true,
		"100.100.100.200":          true,
		"fd00:ec2::254":            true,
		"metadata.google.internal": true,
		"metadata.goog":            true,
		"metadata.azure.internal":  true,
		"instance-data":            true,
	}
)

// EvaluateOutboundEgressGuard enforces SSRF metadata blocking, secret/PII exfiltration airlocks, and domain policies.
func EvaluateOutboundEgressGuard(p *shellguard.HookPayload, wsRoot string) shellguard.HookDecision {
	if p == nil {
		return shellguard.AllowDecision()
	}
	boundary := loadEffectiveBoundaryConfig(wsRoot)
	urls := extractOutboundHTTPURLsForPolicy(p, boundary)
	if len(urls) == 0 {
		return shellguard.AllowDecision()
	}
	rawMaterial := strings.Join([]string{p.TargetURL, p.CommandLine, p.ArgsDigest}, "\n")
	if hasOutboundSecretToken(rawMaterial) {
		return shellguard.DenyDecision("❌ [NOMOS INTERCEPT] Outbound Secret Airlock (Law 7): Credential token detected in outbound HTTP request is strictly prohibited.")
	}
	var gitEmail string
	var loadedEmail bool
	for _, rawURL := range urls {
		host := extractURLHostname(rawURL)
		if host == "" {
			continue
		}
		if isBlockedMetadataHost(host) {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Outbound SSRF Violation (Law 7): Cloud metadata or link-local endpoint '%s' is strictly prohibited.", host))
		}
		if dec := evaluateDomainBoundaryPolicy(host, boundary); dec.Decision == shellguard.DecisionDeny {
			return dec
		}
		if isLoopbackHost(host) {
			continue
		}
		if !loadedEmail {
			gitEmail = resolveWorkspaceGitEmail(wsRoot)
			loadedEmail = true
		}
		if containsEmailExfiltration(rawMaterial, gitEmail) {
			return shellguard.DenyDecision("❌ [NOMOS INTERCEPT] Outbound PII Airlock (Law 7): Operator git email detected in outbound HTTP request is strictly prohibited.")
		}
	}
	return shellguard.AllowDecision()
}

func extractOutboundHTTPURLsForPolicy(p *shellguard.HookPayload, boundary config.BoundaryConfig) []string {
	mode := boundary.EffectiveEgressMode()
	if mode == config.EgressModeOffline || mode == config.EgressModeAllowlist || len(boundary.BlockedEgressDomains) > 0 {
		return extractAllCandidateURLs(p)
	}
	return extractOutboundHTTPURLs(p)
}

func extractAllCandidateURLs(p *shellguard.HookPayload) []string {
	var out []string
	if u := strings.TrimSpace(p.TargetURL); strings.HasPrefix(strings.ToLower(u), "http://") || strings.HasPrefix(strings.ToLower(u), "https://") {
		out = append(out, u)
	}
	if cmd := strings.TrimSpace(p.CommandLine); cmd != "" {
		out = append(out, httpURLTokenRe.FindAllString(cmd, -1)...)
	}
	return out
}

func extractURLHostname(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func extractOutboundHTTPURLs(p *shellguard.HookPayload) []string {
	var out []string
	if u := strings.TrimSpace(p.TargetURL); strings.HasPrefix(strings.ToLower(u), "http://") || strings.HasPrefix(strings.ToLower(u), "https://") {
		out = append(out, u)
	}
	if cmd := strings.TrimSpace(p.CommandLine); cmd != "" && shellNetVerbRe.MatchString(cmd) {
		matches := httpURLTokenRe.FindAllString(cmd, -1)
		out = append(out, matches...)
	}
	return out
}

func hasOutboundSecretToken(material string) bool {
	for _, pat := range egressSecretToks {
		if pat.MatchString(material) {
			return true
		}
	}
	return false
}

func isBlockedMetadataHost(host string) bool {
	if blockedMetadataHosts[host] {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLinkLocalUnicast()
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func evaluateDomainBoundaryPolicy(host string, boundary config.BoundaryConfig) shellguard.HookDecision {
	if domainMatchesPolicyList(host, boundary.BlockedEgressDomains) {
		return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Outbound Egress Policy Violation (Law 7): Domain '%s' is blocked by nomos.yaml blocked_egress_domains.", host))
	}
	if isLoopbackHost(host) {
		return shellguard.AllowDecision()
	}
	switch boundary.EffectiveEgressMode() {
	case config.EgressModeOffline:
		return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Outbound Egress Policy Violation (Law 7): External network egress to '%s' is prohibited when egress_mode is 'offline'.", host))
	case config.EgressModeAllowlist:
		if !domainMatchesPolicyList(host, boundary.AllowedEgressDomains) {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Outbound Egress Policy Violation (Law 7): Domain '%s' is not in nomos.yaml allowed_egress_domains.", host))
		}
	}
	return shellguard.AllowDecision()
}

func domainMatchesPolicyList(host string, patterns []string) bool {
	for _, raw := range patterns {
		pat := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "*.")))
		pat = strings.TrimPrefix(pat, ".")
		if pat == "" {
			continue
		}
		if host == pat || strings.HasSuffix(host, "."+pat) {
			return true
		}
	}
	return false
}

func loadWorkspaceBoundaryConfig(wsRoot string) config.BoundaryConfig {
	cfg, err := config.LoadConfig(wsRoot)
	if err != nil || cfg == nil {
		return config.BoundaryConfig{}
	}
	return cfg.Governance.Boundary
}

func loadEffectiveBoundaryConfig(wsRoot string) config.BoundaryConfig {
	boundary := loadWorkspaceBoundaryConfig(wsRoot)
	pol := LoadSandboxPolicy()
	if boundary.EgressMode == "" && pol.EgressMode != "" {
		boundary.EgressMode = pol.EgressMode
	}
	if len(pol.AllowedEgressDomains) > 0 {
		boundary.AllowedEgressDomains = append(boundary.AllowedEgressDomains, pol.AllowedEgressDomains...)
	}
	if len(pol.BlockedEgressDomains) > 0 {
		boundary.BlockedEgressDomains = append(boundary.BlockedEgressDomains, pol.BlockedEgressDomains...)
	}
	return boundary
}

func resolveWorkspaceGitEmail(wsRoot string) string {
	if strings.TrimSpace(wsRoot) == "" {
		return ""
	}
	out, err := workspace.SafeGitCommand(wsRoot, "config", "--get", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func containsEmailExfiltration(material, email string) bool {
	if email == "" || !strings.Contains(email, "@") {
		return false
	}
	lowerMat := strings.ToLower(material)
	lowerEmail := strings.ToLower(email)
	encodedEmail := strings.ReplaceAll(lowerEmail, "@", "%40")
	return strings.Contains(lowerMat, lowerEmail) || strings.Contains(lowerMat, encodedEmail)
}

// EvaluateCredentialReadGuard blocks unauthorized reads of host or workspace credential files.
func EvaluateCredentialReadGuard(p *shellguard.HookPayload, wsRoot string) shellguard.HookDecision {
	if p == nil || (p.TargetFile == "" && len(p.TargetFiles) == 0 && p.CommandLine == "" && p.ArgsDigest == "") {
		return shellguard.AllowDecision()
	}
	boundary := loadWorkspaceBoundaryConfig(wsRoot)
	dec := capbac.EvaluateCredentialReadAirlock(wsRoot, capbac.ToolFlowInvocation{
		ToolName:    p.ToolName,
		TargetFile:  p.TargetFile,
		CommandLine: p.CommandLine,
		ArgsDigest:  p.ArgsDigest,
	}, p.TargetFiles, capbac.CredentialReadPolicy{
		Mode:             boundary.EffectiveCredentialReadMode(),
		BlockedReadPaths: boundary.BlockedReadPaths,
	})
	if !dec.Allowed {
		return shellguard.DenyDecision(dec.Reason)
	}
	return shellguard.AllowDecision()
}
