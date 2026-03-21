package qualified

import any "io"

func FieldTypeQualified(value any.Reader) {}

var ValueSpecQualified any.Reader

type TypeSpecQualified = any.Reader

func TypeAssertQualified(value interface{}) {
	_ = value.(any.Reader)
}
