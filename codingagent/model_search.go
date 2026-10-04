package codingagent

// Port of modes/interactive/model-search.ts: search-text construction for
// the model selector (provider-prefixed ranking for proxy IDs).

// ModelSearchItem is one searchable model.
type ModelSearchItem struct {
	ID       string
	Provider string
	Name     string
}

// GetModelSearchText builds the catalog search corpus.
func GetModelSearchText(item ModelSearchItem) string {
	name := ""
	if item.Name != "" {
		name = " " + item.Name
	}
	return item.ID + " " + item.Provider + " " + item.Provider + "/" + item.ID + " " + item.Provider + " " + item.ID + name
}

// GetModelSelectorSearchText builds the /model selector corpus: the bare
// model ID stays out of the leading position so exact provider-prefixed
// queries rank before proxy-provider IDs like openrouter/openai/gpt-5.
func GetModelSelectorSearchText(item ModelSearchItem) string {
	name := ""
	if item.Name != "" {
		name = " " + item.Name
	}
	return item.Provider + " " + item.Provider + "/" + item.ID + " " + item.Provider + " " + item.ID + name
}
