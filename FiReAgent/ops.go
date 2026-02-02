// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"sync"
	"time"
)

// OpTracker управляет параллельными операциями и их корректной остановкой
type OpTracker struct {
	mu       sync.Mutex
	stopping bool
	wg       sync.WaitGroup
	stopCh   chan struct{}

	active int // Кол-во активных операций
}

// NewOpTracker создаёт и возвращает новый экземпляр OpTracker
func NewOpTracker() *OpTracker {
	return &OpTracker{
		stopCh: make(chan struct{}),
	}
}

// Start возвращает done-функцию и флаг ok (разрешён ли старт операции)
func (o *OpTracker) Start() (done func(), ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopping {
		return func() {}, false
	}
	o.wg.Add(1)
	o.active++
	return func() {
		// Сначала фиксирует завершение операции в счётчике
		o.mu.Lock()
		if o.active > 0 {
			o.active--
		}
		o.mu.Unlock()
		// Затем сигнализирует wg
		o.wg.Done()
	}, true
}

// IsStopping сообщает, находится ли трекер в состоянии остановки
func (o *OpTracker) IsStopping() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopping
}

// HasActive сообщает, есть ли сейчас активные операции
func (o *OpTracker) HasActive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.active > 0
}

// RequestStop запрещает новые операции и закрывает stopCh после завершения всех активных
func (o *OpTracker) RequestStop() <-chan struct{} {
	o.mu.Lock()
	if !o.stopping {
		o.stopping = true

		// Используется отдельная горутина, чтобы не блокировать основной поток во время ожидания
		go func() {
			o.wg.Wait()
			close(o.stopCh)
		}()
	}
	ch := o.stopCh
	o.mu.Unlock()
	return ch
}

// WaitWithTimeout ждёт завершения всех операций (после RequestStop) с таймаутом
func (o *OpTracker) WaitWithTimeout(timeout time.Duration) bool {
	ch := o.RequestStop()
	if timeout <= 0 {
		<-ch // Без таймаута — просто ждёт окончания всех операций
		return true
	}
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		// log.Printf("Таймаут ожидания завершения активных операций")
		return false
	}
}
