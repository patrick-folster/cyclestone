package resources

import "embed"

//go:embed agents/*.md
var AgentsFS embed.FS

//go:embed creator.md
var CreatorPrompt string

//go:embed safety.md
var SafetyRules string

//go:embed agents/recommender.md
var RecommenderPrompt string
