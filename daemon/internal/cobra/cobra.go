package cobra

import (
	"fmt"
	"io"
	"os"
)

// Command is a minimal Cobra-compatible shim for scaffolded commands.
type Command struct {
	Use          string
	Short        string
	RunE         func(cmd *Command, args []string) error
	SilenceUsage bool

	parent      *Command
	subcommands []*Command
	args        []string
	out         io.Writer
	errOut      io.Writer
}

func (c *Command) AddCommand(cmds ...*Command) {
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		cmd.parent = c
		c.subcommands = append(c.subcommands, cmd)
	}
}

func (c *Command) Execute() error {
	args := os.Args[1:]
	if len(c.args) > 0 {
		args = c.args
	}
	return c.execute(args)
}

func (c *Command) SetArgs(args []string) {
	c.args = append([]string(nil), args...)
}

func (c *Command) OutOrStdout() io.Writer {
	if c.out != nil {
		return c.out
	}
	return os.Stdout
}

func (c *Command) ErrOrStderr() io.Writer {
	if c.errOut != nil {
		return c.errOut
	}
	return os.Stderr
}

func (c *Command) Println(a ...any) {
	fmt.Fprintln(c.OutOrStdout(), a...)
}

func (c *Command) execute(args []string) error {
	if len(args) > 0 {
		if next := c.findSubcommand(args[0]); next != nil {
			return next.execute(args[1:])
		}
		if len(c.subcommands) > 0 && c.RunE == nil {
			return fmt.Errorf("unknown command %q for %q", args[0], c.commandPath())
		}
	}
	if c.RunE != nil {
		return c.RunE(c, args)
	}
	if len(c.subcommands) > 0 {
		return fmt.Errorf("%s requires a subcommand", c.commandPath())
	}
	return nil
}

func (c *Command) findSubcommand(name string) *Command {
	for _, cmd := range c.subcommands {
		if cmd != nil && cmd.Use == name {
			return cmd
		}
	}
	return nil
}

func (c *Command) commandPath() string {
	if c.parent == nil || c.parent.Use == "" {
		return c.Use
	}
	return c.parent.commandPath() + " " + c.Use
}
