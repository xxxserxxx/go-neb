package services

import (
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/google/go-github/github"
	"github.com/matrix-org/go-neb/matrix"
	"github.com/matrix-org/go-neb/plugin"
	"github.com/matrix-org/go-neb/services/github/webhook"
	"github.com/matrix-org/go-neb/types"
	"golang.org/x/oauth2"
	"net/http"
	"regexp"
	"strconv"
)

// Matches alphanumeric then a /, then more alphanumeric then a #, then a number.
// E.g. owner/repo#11 (issue/PR numbers) - Captured groups for owner/repo/number
var ownerRepoIssueRegex = regexp.MustCompile("([A-z0-9-_]+)/([A-z0-9-_]+)#([0-9]+)")

type githubService struct {
	id     string
	UserID string
	Rooms  map[string][]string // room_id => ["push","issue","pull_request"]
}

func (s *githubService) ServiceUserID() string { return s.UserID }
func (s *githubService) ServiceID() string     { return s.id }
func (s *githubService) ServiceType() string   { return "github" }
func (s *githubService) RoomIDs() []string {
	var keys []string
	for k := range s.Rooms {
		keys = append(keys, k)
	}
	return keys
}
func (s *githubService) Plugin(roomID string) plugin.Plugin {
	return plugin.Plugin{
		Commands: []plugin.Command{},
		Expansions: []plugin.Expansion{
			plugin.Expansion{
				Regexp: ownerRepoIssueRegex,
				Expand: func(roomID, matchingText string) interface{} {
					cli := githubClient("")
					owner, repo, num, err := ownerRepoNumberFromText(matchingText)
					if err != nil {
						log.WithError(err).WithField("text", matchingText).Print(
							"Failed to extract owner,repo,number from matched string")
						return nil
					}

					i, _, err := cli.Issues.Get(owner, repo, num)
					if err != nil {
						log.WithError(err).WithFields(log.Fields{
							"owner":  owner,
							"repo":   repo,
							"number": num,
						}).Print("Failed to fetch issue")
						return nil
					}

					return &matrix.TextMessage{
						"m.notice",
						fmt.Sprintf("%s : %s", *i.HTMLURL, *i.Title),
					}
				},
			},
		},
	}
}
func (s *githubService) OnReceiveWebhook(w http.ResponseWriter, req *http.Request, cli *matrix.Client) {
	evType, repo, msg, err := webhook.OnReceiveRequest(req, "")
	if err != nil {
		w.WriteHeader(err.Code)
		return
	}

	for roomID, notif := range s.Rooms {
		notifyRoom := false
		for _, notifyType := range notif {
			if evType == notifyType {
				notifyRoom = true
				break
			}
		}
		if notifyRoom {
			log.WithFields(log.Fields{
				"type":    evType,
				"msg":     msg,
				"repo":    repo,
				"room_id": roomID,
			}).Print("Sending notification to room")
			_, e := cli.SendMessageEvent(roomID, "m.room.message", msg)
			if e != nil {
				log.WithError(e).WithField("room_id", roomID).Print(
					"Failed to send notification to room.")
			}
		}
	}
	w.WriteHeader(200)
}

// githubClient returns a github Client which can perform Github API operations.
// If `token` is empty, a non-authenticated client will be created. This should be
// used sparingly where possible as you only get 60 requests/hour like that (IP locked).
func githubClient(token string) *github.Client {
	var tokenSource oauth2.TokenSource
	if token != "" {
		tokenSource = oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
	}
	httpCli := oauth2.NewClient(oauth2.NoContext, tokenSource)
	return github.NewClient(httpCli)
}

// ownerRepoNumberFromText parses a GH issue string that looks like 'owner/repo#11'
// into its constituient parts. Returns: owner, repo, issue#.
func ownerRepoNumberFromText(ownerRepoNumberText string) (string, string, int, error) {
	// [full_string, owner, repo, issue_number]
	groups := ownerRepoIssueRegex.FindStringSubmatch(ownerRepoNumberText)
	if len(groups) != 4 {
		return "", "", 0, fmt.Errorf("No match found for '%s'", ownerRepoNumberText)
	}
	num, err := strconv.Atoi(groups[3])
	if err != nil {
		return "", "", 0, err
	}
	return groups[1], groups[2], num, nil
}

func init() {
	types.RegisterService(func(serviceID string) types.Service {
		return &githubService{id: serviceID}
	})
}