package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	// "github.com/daeMOn63/bitclient"

	"github.com/daeMOn63/bitclient"
	"github.com/kelseyhightower/envconfig"
	"github.com/slack-go/slack"
	"gopkg.in/yaml.v3"
)

//Config defines settings to connect to bitbucket and projects to scan
type Config struct {
	Bitbucket struct {
		Host     string `yaml:"host" envconfig:"REMINDER_BITBUCKET_HOST"`
		User     string `yaml:"user" envconfig:"REMINDER_BITBUCKET_USER"`
		Password string `envconfig:"REMINDER_BITBUCKET_PASSWORD"`
	} `yaml:"bitbucket"`
	Projects        []Projects `yaml:"projects"`
	FilterReviewers []string   `yaml:"filterReviewers"`
	Slack           struct {
		WebhookURL string `yaml:"webhookURL"  envconfig:"REMINDER_SLACK_URL"`
	} `yaml:"slack"`
}

type Projects struct {
	Key   string   `yaml:"key"`
	Repos []string `yaml:"repos"`
}

type Client struct {
	bitclient *bitclient.BitClient
}

type pullRequestList struct {
	ProjectKey     string
	ProjectName    string
	RepositoryKey  string
	RepositoryName string
	Title          string
	PullRequests   []pullRequest
}

type pullRequest struct {
	Reviewers   []bitclient.Participant
	Link        []bitclient.Link
	CreatedDate uint
	Author      string
}

type projectInfo struct {
	Key  string
	Name string
}

func main() {

	var cfg Config
	readFile(&cfg)
	readEnv(&cfg)
	err := validateConfig(&cfg)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	if !strings.Contains(cfg.Bitbucket.Host, "https://") {
		cfg.Bitbucket.Host = fmt.Sprintf("https://%v/", cfg.Bitbucket.Host)
	}
	cl := newClient(cfg)

	projects := cl.getProjects(&cfg)
	repos := cl.getRepos(&cfg, projects)
	prs := cl.getPullRequests(&repos)
	message := generateSlackMessage(&cfg, &prs)
	sendSlackMessage(&cfg, &message)

	for _, project := range projects.Values {
		fmt.Printf("Project : %d - %s\n", project.Id, project.Key)
	}
}

//newClient creates client for bitbucket server
func newClient(cfg Config) *Client {
	return &Client{bitclient: bitclient.NewBitClient(cfg.Bitbucket.Host, cfg.Bitbucket.User, cfg.Bitbucket.Password)}
}

func readFile(cfg *Config) {
	f, err := os.Open("../config/config.yml")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func readEnv(cfg *Config) {
	err := envconfig.Process("REMINDER", cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func (c *Client) getRepos(cfg *Config, projects *bitclient.GetProjectsResponse) []bitclient.Repository {
	pagedRequst := bitclient.PagedRequest{
		Limit: 100,
	}
	repos := []bitclient.Repository{}
	for _, pr := range projects.Values {
		repo, err := c.bitclient.GetRepositories(pr.Key, pagedRequst)
		if err != nil {
			log.Fatal(err)
		}
		for _, r := range repo.Values {
			repos = append(repos, r)
		}
	}

	return repos
}

func (c *Client) getProjects(cfg *Config) *bitclient.GetProjectsResponse {
	pagedRequest := bitclient.PagedRequest{
		Limit: 100,
	}
	projectResponse, err := c.bitclient.GetProjects(pagedRequest)
	if err != nil {
		log.Fatal(err)
	}
	if len(cfg.Projects) > 0 {
		outProjects := &bitclient.GetProjectsResponse{}
		userProjects := cfg.getConfigProjects()
		for _, pr := range projectResponse.Values {
			for _, ur := range userProjects {
				if pr.Key == ur {
					outProjects.Values = append(outProjects.Values, pr)
				}
			}
		}
		return outProjects
	}

	return &projectResponse
}

func (c *Client) getPullRequests(repos *[]bitclient.Repository) []pullRequestList {
	pagedRequest := bitclient.PagedRequest{
		Limit: 10,
	}
	prRequest := bitclient.GetPullRequestsRequest{
		PagedRequest:   pagedRequest,
		WithProperties: true,
	}
	pullRequests := []pullRequestList{}
	for _, repo := range *repos {
		prResponse, err := c.bitclient.GetPullRequests(repo.Project.Key, repo.Slug, prRequest)
		if err != nil {
			log.Fatal(err)
		}
		if len(prResponse.Values) > 0 {
			for _, responseValues := range prResponse.Values {
				currentPullRequest := pullRequestList{
					ProjectKey:     repo.Project.Key,
					ProjectName:    repo.Project.Name,
					RepositoryKey:  repo.Slug,
					RepositoryName: repo.Name,
					Title:          responseValues.Title,
				}
				currentPullRequestInfo := pullRequest{
					Author:      responseValues.Author.User.Slug,
					CreatedDate: responseValues.CreatedDate,
				}
				currentPullRequestInfo.Link = responseValues.Links["self"]
				for _, reviwer := range responseValues.Reviewers {
					currentPullRequestInfo.Reviewers = append(currentPullRequestInfo.Reviewers, reviwer)
				}
				currentPullRequest.PullRequests = append(currentPullRequest.PullRequests, currentPullRequestInfo)
				pullRequests = append(pullRequests, currentPullRequest)
			}
		}
	}
	return pullRequests

}

func (cfg *Config) getConfigProjects() (out []string) {
	for _, value := range cfg.Projects {
		out = append(out, value.Key)
	}
	return out
}

func generateSlackMessage(cfg *Config, pullRequests *[]pullRequestList) slack.WebhookMessage {
	var messageBlocks []slack.Block
	divSection := slack.NewDividerBlock()
	headerText := slack.NewTextBlockObject("plain_text", "Время поревьюить :party-parrot:", true, false)
	headerSection := slack.NewSectionBlock(headerText, nil, nil)
	messageBlocks = append(messageBlocks, headerSection)
	for _, pr := range *pullRequests {
		reviewers := ""
		reviewersList := filterReviewers(cfg, pr.PullRequests[0].Reviewers)
		for _, rev := range reviewersList {
			if rev.Status == "UNAPPROVED" {
				reviewers += rev.User.Name + " "
			}

		}
		messageText := fmt.Sprintf("*Project:* %v %v\n*Repository:* %v\n*<%v|%v>*\n*To review:* %v", pr.ProjectName, pr.ProjectKey, pr.RepositoryName, pr.PullRequests[0].Link[0]["href"], pr.Title, reviewers)
		messageObject := slack.NewTextBlockObject("mrkdwn", messageText, false, false)
		messageSection := slack.NewSectionBlock(messageObject, nil, nil)

		messageBlocks = append(messageBlocks, messageSection)
		messageBlocks = append(messageBlocks, divSection)
	}

	message := slack.WebhookMessage{
		Blocks: &slack.Blocks{
			BlockSet: messageBlocks,
		},
	}

	return message
}

func sendSlackMessage(cfg *Config, message *slack.WebhookMessage) {
	err := slack.PostWebhook(cfg.Slack.WebhookURL, message)
	if err != nil {
		log.Fatal(err)
	}
}

func filterReviewers(cfg *Config, reviewerList []bitclient.Participant) []bitclient.Participant {
	var toReturn []bitclient.Participant
	for _, reviewer := range reviewerList {
		counter := 0
		for _, ft := range cfg.FilterReviewers {
			if reviewer.User.Name == ft {
				counter++
			}
		}
		if counter == 0 {
			toReturn = append(toReturn, reviewer)
		}
	}
	return toReturn
}

func validateConfig(cfg *Config) error {
	if cfg.Slack.WebhookURL == "" {
		return errors.New("webhookURL should be set either in config or as REMINDER_SLACK_URL environment variable")
	}
	return nil
}
