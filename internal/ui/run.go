package ui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
)

func Run(ctx context.Context, in io.Reader, out io.Writer, environment []string, model tea.Model) error {
	_, err := runProgram(ctx, in, out, environment, model)
	return err
}

func runProgram(ctx context.Context, in io.Reader, out io.Writer, environment []string, model tea.Model) (tea.Model, error) {
	return tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithEnvironment(environment)).Run()
}
