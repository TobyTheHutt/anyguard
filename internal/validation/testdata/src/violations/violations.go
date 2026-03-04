package violations

type Payload map[string]any // want "disallowed any usage"

func Consume(value any) {} // want "disallowed any usage"

type Box[T any] struct{}

type Silent map[string]any //nolint:anyguard
