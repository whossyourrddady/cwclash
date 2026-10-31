// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"errors"
)

var errProfileBusy = errors.New("此配置正在处理中，请等待完成")

func (a *App) beginProfileTask(parent context.Context, id string) (context.Context, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.profileTasks == nil {
		a.profileTasks = map[string]context.CancelFunc{}
	}
	if _, exists := a.profileTasks[id]; exists {
		return nil, nil, errProfileBusy
	}
	ctx, cancel := context.WithCancel(parent)
	a.profileTasks[id] = cancel
	return ctx, func() {
		cancel()
		a.mu.Lock()
		delete(a.profileTasks, id)
		a.mu.Unlock()
	}, nil
}

func (a *App) cancelProfileTasks() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, cancel := range a.profileTasks {
		cancel()
	}
}
