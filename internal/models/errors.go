package models

// RecoverableError is implemented by enriched errors that carry structured
// context and remediation hints. Both the store and output packages use this
// interface to avoid an import cycle.
type RecoverableError interface {
	error
	ErrorCode() string
	Context() map[string]string
	SuggestedAction() string
}
