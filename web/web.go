package web

import (
	"html/template"
	"math/rand"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/appengine"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/user"

	"golang.org/x/net/context"

	"github.com/lnsp/zwig/utils"

	"github.com/lnsp/zwig/models"
)

// the duration of the auth cookie
const (
	authCookieDuration = 608400
)

// collection of template file names
const (
	baseTemplateFile = "static/templates/base.html"
	showTemplateFile = "static/templates/show.html"
	listTemplateFile = "static/templates/list.html"
)

// collection of available colors
var (
	colors = [4]string{"blue", "red", "orange", "green"}
)

type authHandleFunc func(http.ResponseWriter, *http.Request, bool, string)

// Handler presents a Web UI to interact with posts.
type Handler struct {
	mux                *http.ServeMux
	listTmpl, showTmpl *template.Template
}

// New initializes a new web handler.
func New() *Handler {
	mux := http.NewServeMux()
	web := &Handler{mux, nil, nil}
	// load templates
	web.listTmpl = template.Must(template.ParseFiles(baseTemplateFile, listTemplateFile))
	web.showTmpl = template.Must(template.ParseFiles(baseTemplateFile, showTemplateFile))
	// add dynamic routes
	mux.Handle("/", web.auth(web.list, false))
	mux.Handle("/comments", web.auth(web.comments, false))
	mux.Handle("/post", web.auth(web.post, true))
	mux.Handle("/vote", web.auth(web.vote, true))
	mux.HandleFunc("/auth/logout", web.logout)
	return web
}

func (handler *Handler) logout(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	logoutURL, _ := user.LogoutURL(c, "/")
	http.Redirect(w, r, logoutURL, http.StatusFound)
}

// ServeHTTP serves HTTP requests.
func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handler.mux.ServeHTTP(w, r)
}

// template-internal post representation
type postItem struct {
	User         string `json:"user"`
	Text         string `json:"text"`
	Topic        int64  `json:"topic"`
	Votes        int    `json:"votes"`
	Color        string `json:"color"`
	Post         int64  `json:"post"`
	OwnPost      bool   `json:"own"`
	HasUpvoted   bool   `json:"upvoted"`
	Voted        bool   `json:"voted"`
	HasDownvoted bool   `json:"downvoted"`
	SincePost    string `json:"since"`
}

func (handler *Handler) list(w http.ResponseWriter, r *http.Request, auth bool, user string) {
	c := appengine.NewContext(r)
	posts, ids, err := models.TopPosts(c, 30, -10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]postItem, len(posts))
	for i := range posts {
		items[i] = handler.toPostItem(c, ids[i], posts[i], user)
	}
	if err := handler.listTmpl.Execute(w, struct {
		Karma     int
		NextColor string
		Posts     []postItem
		Main      string
		User      string
	}{
		Karma:     models.GetKarma(c, user),
		NextColor: colors[rand.Intn(len(colors))],
		Posts:     items,
		Main:      "",
		User:      user,
	}); err != nil {
		http.Error(w, "Failed to render template: "+err.Error(), http.StatusInternalServerError)
	}
}

func (handler *Handler) comments(w http.ResponseWriter, r *http.Request, auth bool, user string) {
	c := appengine.NewContext(r)
	reqID := strings.TrimSpace(r.URL.Query().Get("id"))
	id, err := strconv.ParseInt(reqID, 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	post, err := models.GetPost(c, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	comments, ids, err := models.GetComments(c, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]postItem, len(comments))
	for i := range comments {
		items[i] = handler.toPostItem(c, ids[i], comments[i], user)
	}
	main := handler.toPostItem(c, id, post, user)
	if err := handler.showTmpl.Execute(w, struct {
		Karma     int
		NextColor string
		Main      postItem
		Comments  []postItem
		User      string
	}{
		Karma:     models.GetKarma(c, user),
		NextColor: colors[rand.Intn(len(colors))],
		Main:      main,
		Comments:  items,
		User:      user,
	}); err != nil {
		http.Error(w, "Failed to render template: "+err.Error(), http.StatusInternalServerError)
	}
}

func (handler *Handler) post(w http.ResponseWriter, r *http.Request, auth bool, user string) {
	if !auth {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	c := appengine.NewContext(r)
	text := r.FormValue("text")
	color := r.FormValue("color")
	topic := r.FormValue("topic")
	keep := r.FormValue("keep")
	log.Debugf(c, "web.post: user=%s color=%s topic=%s keep=%s\n", user, color, topic, keep)
	redirectURL := "/"
	if keep != "" {
		redirectURL = "/comments?id=" + topic
	}
	if strings.TrimSpace(text) == "" {
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}
	parent, err := strconv.ParseInt(topic, 10, 64)
	if topic != "" && err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := models.SubmitPost(c, user, text, color, parent); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

func (handler *Handler) vote(w http.ResponseWriter, r *http.Request, auth bool, user string) {
	c := appengine.NewContext(r)
	if !auth {
		log.Debugf(c, "web.vote: user not authorized")
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	upvote := r.FormValue("upvote")
	downvote := r.FormValue("downvote")
	post := r.FormValue("post")
	keep := r.FormValue("keep")
	topic := r.FormValue("topic")
	id, err := strconv.ParseInt(post, 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Debugf(c, "web.vote: post=%s keep=%s topic=%s upvote=%s downvote%s\n", post, keep, topic, upvote, downvote)
	redirectURL := "/"
	if keep != "" {
		if topic != "" {
			redirectURL = "/comments?id=" + topic
		} else {
			redirectURL = "/comments?id=" + post
		}
	}
	state := upvote != ""
	if _, err := models.SubmitVote(c, user, id, state); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

type authMiddleware struct {
	handler  authHandleFunc
	required bool
}

func (auth authMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	if u := user.Current(c); u != nil {
		auth.handler(w, r, true, u.Email)
	} else if auth.required {
		loginURL, _ := user.LoginURL(c, r.URL.String())
		http.Redirect(w, r, loginURL, http.StatusFound)
	} else {
		auth.handler(w, r, false, "")
	}
}

func (handler *Handler) auth(f authHandleFunc, require bool) *authMiddleware {
	return &authMiddleware{
		handler:  f,
		required: require,
	}
}

func (handler *Handler) toPostItem(c context.Context, id int64, post models.Post, user string) postItem {
	vote, err := models.GetVoteBy(c, id, user)
	numVotes, _ := models.NumberOfVotes(c, id)

	return postItem{
		Post:         id,
		User:         post.Author,
		Text:         post.Text,
		Votes:        numVotes,
		Color:        post.Color,
		Topic:        post.Parent,
		OwnPost:      post.Author == user,
		HasUpvoted:   err == nil && vote.Upvote,
		HasDownvoted: err == nil && !vote.Upvote,
		SincePost:    utils.HumanTimeFormat(post.Date),
		Voted:        err == nil,
	}
}
