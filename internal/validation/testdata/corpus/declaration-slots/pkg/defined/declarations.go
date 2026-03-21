package defined

type any int

func FieldTypeDefined(value any) {}

var ValueSpecDefined any

type TypeSpecDefined = any

func TypeAssertDefined(value interface{}) {
	_ = value.(any)
}
