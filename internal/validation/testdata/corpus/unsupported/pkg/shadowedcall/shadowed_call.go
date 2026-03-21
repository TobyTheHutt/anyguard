package shadowedcall

func any(v int) int {
	return v
}

func Use() {
	_ = any(1)
}
