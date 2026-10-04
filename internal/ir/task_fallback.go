package ir

// taskPrimitiveFallback is what each task primitive answers in this
// compiler's blocking fallback (docs/NET-P3-SUSPENSION-PLAN.md §3.6): one
// task id, no current task, a leave that reports the running mode, and a
// park answer that is never read because a park needs a current task.
var taskPrimitiveFallback = map[string]int32{
	"__task_new":           1,
	"__task_free":          0,
	"__task_cur":           0,
	"__task_enter":         0,
	"__task_leave":         0,
	"__task_park":          -1,
	"__task_token":         0,
	"__task_set_ready":     0,
	"__task_set_cancelled": 0,
}

// taskPrimitiveVoid names the primitives whose result is void.
var taskPrimitiveVoid = map[string]bool{
	"__task_free":          true,
	"__task_enter":         true,
	"__task_set_ready":     true,
	"__task_set_cancelled": true,
}
