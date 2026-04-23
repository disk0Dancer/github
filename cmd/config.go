package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	cfg "github/internal/config"
)

var configSetSecret bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage local CLI configuration",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List properties in the active configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}

		type entry struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Secret bool   `json:"secret"`
		}
		type resp struct {
			Path    string  `json:"path"`
			Active  string  `json:"active_profile"`
			Entries []entry `json:"entries"`
		}

		masked := store.MaskedEntries()
		entries := make([]entry, 0, len(masked))
		for _, item := range masked {
			entries = append(entries, entry{Key: item.Key, Value: item.Value, Secret: item.Secret})
		}

		writeJSON(resp{
			Path:    store.Path(),
			Active:  store.ActiveProfileName(),
			Entries: entries,
		})
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a property in the active configuration",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}
		key := args[0]
		value := args[1]

		store.Set(key, value, configSetSecret)
		if err := store.Save(); err != nil {
			return err
		}

		type resp struct {
			Active  string `json:"active_profile"`
			Updated string `json:"updated"`
			Secret  bool   `json:"secret"`
		}
		writeJSON(resp{
			Active:  store.ActiveProfileName(),
			Updated: key,
			Secret:  configSetSecret,
		})
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a property from the active configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}
		key := args[0]
		value, ok := store.Get(key)
		if !ok {
			return fmt.Errorf("property %q is not set in profile %q", key, store.ActiveProfileName())
		}

		type resp struct {
			Active string `json:"active_profile"`
			Key    string `json:"key"`
			Value  string `json:"value"`
		}
		writeJSON(resp{Active: store.ActiveProfileName(), Key: key, Value: value})
		return nil
	},
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Unset a property in the active configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}
		key := args[0]
		removed := store.Unset(key)
		if err := store.Save(); err != nil {
			return err
		}

		type resp struct {
			Active  string `json:"active_profile"`
			Removed string `json:"removed"`
			Found   bool   `json:"found"`
		}
		writeJSON(resp{Active: store.ActiveProfileName(), Removed: key, Found: removed})
		return nil
	},
}

var configProfilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "Manage named profiles",
}

var configProfilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List profile names",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}

		type item struct {
			Name   string `json:"name"`
			Active bool   `json:"active"`
		}
		type resp struct {
			Profiles []item `json:"profiles"`
		}

		items := make([]item, 0, len(store.ProfileNames()))
		for _, name := range store.ProfileNames() {
			items = append(items, item{Name: name, Active: name == store.ActiveProfileName()})
		}
		writeJSON(resp{Profiles: items})
		return nil
	},
}

var configProfilesCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a named profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}
		if err := store.CreateProfile(args[0]); err != nil {
			return err
		}
		if err := store.Save(); err != nil {
			return err
		}

		type resp struct {
			Created string `json:"created"`
		}
		writeJSON(resp{Created: args[0]})
		return nil
	},
}

var configProfilesUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Use a named profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}
		if err := store.UseProfile(args[0]); err != nil {
			return err
		}
		if err := store.Save(); err != nil {
			return err
		}

		type resp struct {
			Active string `json:"active_profile"`
		}
		writeJSON(resp{Active: store.ActiveProfileName()})
		return nil
	},
}

func init() {
	configSetCmd.Flags().BoolVar(&configSetSecret, "secret", false, "Store the value as a secret and mask it in config list")

	configProfilesCmd.AddCommand(configProfilesListCmd)
	configProfilesCmd.AddCommand(configProfilesCreateCmd)
	configProfilesCmd.AddCommand(configProfilesUseCmd)

	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configUnsetCmd)
	configCmd.AddCommand(configProfilesCmd)
	rootCmd.AddCommand(configCmd)
}
