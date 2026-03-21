package alias

type any = string

func FieldTypeAlias(value any) {}

var ValueSpecAlias any

type TypeSpecAlias = any

func TypeAssertAlias(value interface{}) {
	_ = value.(any)
}
