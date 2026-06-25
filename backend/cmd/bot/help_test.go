package main

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// TestHelpCoversEverySubcommand enforces the rule that /coreteam help documents
// every /coreteam subcommand: each subcommand (or subcommand group) registered
// on coreTeamCommand must have a helpCommands entry, and every helpCommands
// entry must map to a real subcommand. This keeps the in-Discord help guide in
// sync automatically — when you add, rename, or remove a bot command, update
// helpCommands in cmd/bot/help.go or this test fails.
func TestHelpCoversEverySubcommand(t *testing.T) {
	subcommands := map[string]bool{}
	for _, opt := range coreTeamCommand.Options {
		switch opt.Type {
		case discordgo.ApplicationCommandOptionSubCommand,
			discordgo.ApplicationCommandOptionSubCommandGroup:
			subcommands[opt.Name] = true
		}
	}

	if len(subcommands) == 0 {
		t.Fatal("coreTeamCommand has no subcommands — did the command shape change?")
	}

	for name := range subcommands {
		if findHelpCommand(name) == nil {
			t.Errorf("/coreteam %s has no helpCommands entry — add one in cmd/bot/help.go", name)
		}
	}

	for _, hc := range helpCommands {
		if !subcommands[hc.Name] {
			t.Errorf("helpCommands has %q but there is no matching /coreteam subcommand — remove or rename it in cmd/bot/help.go", hc.Name)
		}
	}
}
