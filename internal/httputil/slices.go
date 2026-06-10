package httputil

func EmptySlice[T any](values []T) []T {
	if values == nil {
		return make([]T, 0)
	}
	return values
}

func EmptyMap[K comparable, V any](values map[K]V) map[K]V {
	if values == nil {
		return make(map[K]V)
	}
	return values
}
