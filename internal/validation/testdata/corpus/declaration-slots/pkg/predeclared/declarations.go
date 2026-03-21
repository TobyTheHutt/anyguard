package predeclared

func FieldTypePredeclared(value any) {}

var ValueSpecPredeclared any

type TypeSpecPredeclared = any

func TypeAssertPredeclared(value interface{}) {
	_ = value.(any)
}
