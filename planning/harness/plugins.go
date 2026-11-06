package harness

import (
	"context"
	"errors"
	"nofx/planning/model"
	"nofx/planning/plugins"
)

func (e *Engine) tools() []model.Tool {
	tools := Tools()
	if e.Plugins == nil {
		return tools
	}
	ids := []string{}
	for _, m := range e.Plugins.List() {
		if m.Enabled {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return tools
	}
	var t model.Tool
	t.Type = "function"
	t.Function.Name = "get_data_plugin"
	t.Function.Description = "Read an enabled public data plugin for the fixed workspace. Source, time and native units are returned. No trade or approval authority."
	t.Function.Parameters = object(map[string]any{"plugin": map[string]any{"type": "string", "enum": ids}}, "plugin")
	return append(tools, t)
}
func (e *Engine) readPlugin(ctx context.Context, input Input, id string, emit Emit) (plugins.Result, error) {
	if e.Plugins == nil {
		return plugins.Result{}, errors.New("no data plugin registry")
	}
	data, err := e.Plugins.Read(ctx, id, plugins.Request{Venue: input.Venue, Symbol: input.Symbol, Window: input.PluginWindow})
	if ctx.Err() != nil {
		return data, ctx.Err()
	}
	if err != nil {
		if emitErr := emit(Event{"plugin_data", map[string]string{"plugin": id, "error": err.Error()}}); emitErr != nil {
			return data, emitErr
		}
		return data, err
	}
	return plugins.ModelContext(data), emit(Event{"plugin_data", plugins.ModelContext(data)})
}
