package predeclared

func LocalPredeclared(value interface{}) {
	var local any
	type LocalAlias = any
	_ = local
	_ = value.(any)
	_ = func(item any) {}
}
