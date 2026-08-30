package instruction

import (
	"webhookdispatcher/internal/application/entity"
)

// scheduleRetry — инструкция планирования следующей попытки доставки.
type scheduleRetry struct{}

// NewScheduleRetry возвращает инструкцию планирования.
func NewScheduleRetry() *scheduleRetry { return &scheduleRetry{} }

// Invoke переводит доставку по статусу ответа: 2xx → DELIVERED;
// иначе RETRYING до лимита попыток и далее DEAD_LETTER. Возвращает исход.
func (scheduleRetry) Invoke(d *entity.Delivery, httpStatus int) (entity.Outcome, error) {
	o := entity.OutcomeFromStatus(httpStatus)
	if o == entity.OutcomeRetry && d.Attempt >= entity.MaxAttempts {
		o = entity.OutcomeDead
	}
	d.ScheduleFrom(o)
	return o, nil
}