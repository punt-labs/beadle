package daemon

import (
	"context"
	"fmt"
	"regexp"
)

// CommandCall is one step in a planned pipeline.
type CommandCall struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args"`
}

// Planner decomposes an email instruction into a sequence of commands.
type Planner interface {
	Plan(ctx context.Context, meta EmailMeta, body string) ([]CommandCall, error)
}

// RuleEntry pairs a regex pattern with the commands to execute on match.
type RuleEntry struct {
	Pattern  string        // regex matched against subject + "\n" + body
	Commands []CommandCall // commands returned when Pattern matches
}

type compiledRule struct {
	re       *regexp.Regexp
	commands []CommandCall
}

// RulePlanner matches email content against an ordered list of regex rules.
// First match wins.
type RulePlanner struct {
	rules []compiledRule
}

// NewRulePlanner compiles each rule's pattern and returns a RulePlanner.
// Returns an error if any pattern fails to compile.
func NewRulePlanner(rules []RuleEntry) (*RulePlanner, error) {
	compiled := make([]compiledRule, len(rules))
	for i, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile rule %d pattern %q: %w", i, r.Pattern, err)
		}
		compiled[i] = compiledRule{re: re, commands: r.Commands}
	}
	return &RulePlanner{rules: compiled}, nil
}

// Plan iterates rules in order and returns the commands for the first match.
// The pattern is matched against meta.Subject + "\n" + body.
func (p *RulePlanner) Plan(_ context.Context, meta EmailMeta, body string) ([]CommandCall, error) {
	text := meta.Subject + "\n" + body
	for _, r := range p.rules {
		if r.re.MatchString(text) {
			return r.commands, nil
		}
	}
	return nil, fmt.Errorf("no rule matches this instruction")
}

// LLMPlanner is a stub for future LLM-based planning.
type LLMPlanner struct{}

// Plan is not yet implemented.
func (p *LLMPlanner) Plan(_ context.Context, _ EmailMeta, _ string) ([]CommandCall, error) {
	return nil, fmt.Errorf("LLMPlanner not yet implemented")
}

// StubPlanner returns preconfigured results, for use in tests.
type StubPlanner struct {
	Result []CommandCall
	Err    error
}

// Plan returns the preconfigured Result and Err.
func (p *StubPlanner) Plan(_ context.Context, _ EmailMeta, _ string) ([]CommandCall, error) {
	return p.Result, p.Err
}
