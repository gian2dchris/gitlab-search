package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gian/gitlab-search/internal/gitlab"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "gitlab-search",
	Short: "A CLI tool to search GitLab for code and resources",
	Long: `GitLab Search CLI allows you to perform global searches across GitLab projects,
groups, and repositories, specifically designed for GitLab CE instances that lack
native global search features.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		token := viper.GetString("token")
		searchQuery := viper.GetString("search")
		limit := viper.GetFloat64("limit")
		verbose := viper.GetBool("verbose")

		if host == "" || token == "" || searchQuery == "" {
			return fmt.Errorf("host, token, and search query are required")
		}

		// Print configuration header
		fmt.Printf("\033[1;36m--- Configuration ---\033[0m\n")
		fmt.Printf("Host:       %s\n", host)
		fmt.Printf("Search:     '%s'\n", searchQuery)
		fmt.Printf("Rate Limit: %.1f req/sec\n", limit)
		fmt.Printf("Verbose:     %t\n", verbose)
		if file := viper.GetString("file"); file != "" {
			fmt.Printf("File Match: %s\n", file)
		}
		if projects := viper.GetStringSlice("project"); len(projects) > 0 {
			fmt.Printf("Projects:   %s\n", strings.Join(projects, ", "))
		}
		if groups := viper.GetStringSlice("group"); len(groups) > 0 {
			fmt.Printf("Groups:     %s\n", strings.Join(groups, ", "))
		}
		fmt.Printf("\033[1;36m---------------------\033[0m\n\n")

		client, err := gitlab.NewClient(host, token, limit)
		if err != nil {
			return err
		}

		opts := gitlab.SearchOptions{
			Query:    searchQuery,
			File:     viper.GetString("file"),
			Projects: viper.GetStringSlice("project"),
			Groups:   viper.GetStringSlice("group"),
		}

		results, err := client.SearchGitlab(opts)
		if err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		// Group results by project
		grouped := make(map[string][]gitlab.SearchResult)
		for _, res := range results {
			grouped[res.ProjectName] = append(grouped[res.ProjectName], res)
		}

		// Sort project names for consistent output
		var projectNames []string
		for name := range grouped {
			projectNames = append(projectNames, name)
		}
		sort.Strings(projectNames)

		fmt.Printf("\nFound %d matches across %d projects:\n\n", len(results), len(projectNames))

		for _, name := range projectNames {
			projectResults := grouped[name]
			// Use the first result to get the project search URL
			projectURL := projectResults[0].ProjectURL

			fmt.Printf("\033[1;34m%s\033[0m\n", name)
			if projectURL != "" {
				fmt.Printf("\033[0;37m %s\033[0m\n", projectURL)
			}
			fmt.Println(strings.Repeat("-", len(name)))

			if verbose {

				for _, res := range projectResults {
					var typeColor int
					var stateColor int

					if res.Type == "CODE" {
						typeColor = 34
						stateColor = 37
					}

					if res.Type == "ISSUE" {
						typeColor = 33
						switch res.State {
						case "opened":
							stateColor = 32
						case "closed":
							stateColor = 31
						}
					}
					if res.Type == "MR" {
						typeColor = 35
						switch res.State {
						case "opened":
							stateColor = 32
						case "merged":
							stateColor = 34
						case "closed":
							stateColor = 31
						}
					}

					// [\033[1;%dm%s\033[0m]\n",
					fmt.Printf("[\033[1;%dm%s\033[0m] \033[1;37m%s\033[0m", typeColor, res.Type, res.Title)
					if res.Type == "CODE" {
						fmt.Println()
					} else {
						fmt.Printf(" [\033[1;%dm%s\033[0m]\n", stateColor, res.State)
					}
					fmt.Printf("%s\n", res.URL)
					fmt.Println()
				}
			}
			fmt.Println()
		}

		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./config.yaml)")
	rootCmd.PersistentFlags().Float64P("limit", "l", 2.0, "GitLab API rate limit (requests per second)")

	rootCmd.Flags().StringP("host", "H", "", "GitLab host URL (e.g. https://gitlab.com)")
	rootCmd.Flags().StringP("token", "t", "", "GitLab Personal Access Token")
	rootCmd.Flags().StringP("search", "s", "", "Search query/regex")
	rootCmd.Flags().StringP("file", "f", "", "File path pattern filter (e.g. *.php)")
	rootCmd.Flags().StringSliceP("project", "p", []string{}, "Project filter (comma-separated)")
	rootCmd.Flags().StringSliceP("group", "g", []string{}, "Group filter (comma-separated)")
	rootCmd.Flags().BoolP("verbose", "v", false, "Print verbose output")
	// Bind flags to viper
	viper.BindPFlag("host", rootCmd.Flags().Lookup("host"))
	viper.BindPFlag("token", rootCmd.Flags().Lookup("token"))
	viper.BindPFlag("search", rootCmd.Flags().Lookup("search"))
	viper.BindPFlag("file", rootCmd.Flags().Lookup("file"))
	viper.BindPFlag("project", rootCmd.Flags().Lookup("project"))
	viper.BindPFlag("group", rootCmd.Flags().Lookup("group"))
	viper.BindPFlag("limit", rootCmd.PersistentFlags().Lookup("limit"))
	viper.BindPFlag("verbose", rootCmd.Flags().Lookup("verbose"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Look for config.yaml in the current directory
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("GITLAB_SEARCH")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}
