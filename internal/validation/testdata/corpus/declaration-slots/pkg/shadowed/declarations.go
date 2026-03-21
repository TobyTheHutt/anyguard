package shadowed

type any interface{}

func FieldTypeShadowed(value any) {}

var ValueSpecShadowed any

type TypeSpecShadowed = any

func TypeAssertShadowed(value interface{}) {
	_ = value.(any)
}
