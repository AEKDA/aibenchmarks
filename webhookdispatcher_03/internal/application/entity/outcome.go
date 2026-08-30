package entity

// Outcome решение по HTTP-статусу ответа подписчика.
type Outcome int

// Итоги доставки.
const (
	OutcomeDelivered Outcome = iota // 2xx
	OutcomeRetry                    // прочие статусы — retry
	OutcomeDead                     // у задачи исчерпаны попытки
)

// OutcomeFromStatus возвращает Outcome по http-статусу согласно политике
// (2xx → delivered; все остальные коды, включая 429/4xx/5xx, → retry).
func OutcomeFromStatus(code int) Outcome {
	if code >= 200 && code < 300 {
		return OutcomeDelivered
	}
	return OutcomeRetry
}
