package shadowedtype

type any interface{}

type Box[T, U any] struct{}

func Use() {
	_ = Box[int, any]{}
}
