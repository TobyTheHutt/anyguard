package api

type NestedArray map[string][]any

type NestedMap []map[string]any

func Boundary(values ...[]any) map[string][]any {
	var typed []map[any]string
	type Local = map[string][]any
	_ = typed
	return nil
}
