package operations

// promptSet is the three prompts passed to one image generation.
//
// System, user and negative are always returned together by the same implementation
// (ports.PanelPrompt / ports.PagePrompt), so they travel as one value rather than as
// three separate return values.
type promptSet struct {
	system   string
	user     string
	negative string
}

// newPromptSet takes the return values of the ports Build methods as is.
func newPromptSet(system, user, negative string, err error) (promptSet, error) {
	if err != nil {
		return promptSet{}, err
	}
	return promptSet{system: system, user: user, negative: negative}, nil
}

// withOverride replaces the user prompt only when the caller overrode it
// (GenerateOptions.PromptOverride). The system instruction and the negative prompt keep
// the model's output in shape, so they are not replaced.
func (p promptSet) withOverride(override string) promptSet {
	if override != "" {
		p.user = override
	}
	return p
}
