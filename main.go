package main

import (
	"flag"
	"html/template"
	"net/http"

	"path/filepath"

	"os"
	"strings"

	"github.com/cihub/seelog"
	"github.com/claudiu/gocron"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"wblog/controllers"
	"wblog/helpers"
	"wblog/models"
	"wblog/system"
)

func main() {

	//1.加载配置文件.
	configFilePath, logConfigPath := LoadConfigFiles()

	//2/加载配置log.
	logger, err := seelog.LoggerFromConfigAsFile(*logConfigPath)
	if err != nil {
		seelog.Critical("err parsing seelog config file", err)
		return
	}
	seelog.ReplaceLogger(logger)
	defer seelog.Flush()

	//3.加载yaml配置文件进全局结构体:system.Configuration
	if err := system.LoadConfiguration(*configFilePath); err != nil {
		seelog.Critical("err parsing config log file", err)
		return
	}

	//4.创建DB连接,表,索引.
	db, err := models.InitDB()
	if err != nil {
		seelog.Critical("err open databases", err)
		return
	}
	defer db.Close()

	//5.配置routers
	router := SetRouters()

	//6.配置定时任务.
	SetJobs()

	//7.启动系统.
	router.Run(system.GetConfiguration().Addr)
}

func LoadConfigFiles() (*string, *string) {
	configFilePath := flag.String("C", "conf/conf.yaml", "config file path")
	logConfigPath := flag.String("L", "conf/seelog.xml", "log config file path")
	flag.Parse()
	return configFilePath, logConfigPath
}

func SetRouters() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	setTemplate(router)
	setSessions(router)
	router.Use(SharedData())
	SetStaticRouter(router)
	SetRootRouter(router)
	SetVisitorRouterGroup(router)
	SetAdminRouterGroup(router)
	return router
}

func SetStaticRouter(router *gin.Engine) gin.IRoutes {
	return router.Static("/static", filepath.Join(getCurrentDirectory(), "./static"))
}

func SetRootRouter(router *gin.Engine) {
	router.NoRoute(controllers.Handle404)
	router.GET("/", controllers.IndexGet)
	router.GET("/index", controllers.IndexGet)
	router.GET("/rss", controllers.RssGet)

	if system.GetConfiguration().SignupEnabled {
		router.GET("/signup", controllers.SignupGet)
		router.POST("/signup", controllers.SignupPost)
	}
	// user signin and logout
	router.GET("/signin", controllers.SigninGet)
	router.POST("/signin", controllers.SigninPost)
	router.GET("/logout", controllers.LogoutGet)
	router.GET("/oauth2callback", controllers.Oauth2Callback)
	router.GET("/auth/:authType", controllers.AuthGet)

	// captcha
	router.GET("/captcha", controllers.CaptchaGet)

	// subscriber
	router.GET("/subscribe", controllers.SubscribeGet)
	router.POST("/subscribe", controllers.Subscribe)
	router.GET("/active", controllers.ActiveSubscriber)
	router.GET("/unsubscribe", controllers.UnSubscribe)

	router.GET("/page/:id", controllers.PageGet)
	router.GET("/post/:id", controllers.PostGet)
	router.GET("/tag/:tag", controllers.TagGet)
	router.GET("/archives/:year/:month", controllers.ArchiveGet)

	router.GET("/link/:id", controllers.LinkGet)
}

func SetAdminRouterGroup(router *gin.Engine) {
	authorized := router.Group("/admin")
	authorized.Use(AdminScopeRequired())
	{
		// index
		authorized.GET("/index", controllers.AdminIndex)

		// image upload
		authorized.POST("/upload", controllers.Upload)

		// page
		authorized.GET("/page", controllers.PageIndex)
		authorized.GET("/new_page", controllers.PageNew)
		authorized.POST("/new_page", controllers.PageCreate)
		authorized.GET("/page/:id/edit", controllers.PageEdit)
		authorized.POST("/page/:id/edit", controllers.PageUpdate)
		authorized.POST("/page/:id/publish", controllers.PagePublish)
		authorized.POST("/page/:id/delete", controllers.PageDelete)

		// post
		authorized.GET("/post", controllers.PostIndex)
		authorized.GET("/new_post", controllers.PostNew)
		authorized.POST("/new_post", controllers.PostCreate)
		authorized.GET("/post/:id/edit", controllers.PostEdit)
		authorized.POST("/post/:id/edit", controllers.PostUpdate)
		authorized.POST("/post/:id/publish", controllers.PostPublish)
		authorized.POST("/post/:id/delete", controllers.PostDelete)

		// tag
		authorized.POST("/new_tag", controllers.TagCreate)

		//
		authorized.GET("/user", controllers.UserIndex)
		authorized.POST("/user/:id/lock", controllers.UserLock)

		// profile
		authorized.GET("/profile", controllers.ProfileGet)
		authorized.POST("/profile", controllers.ProfileUpdate)
		authorized.POST("/profile/email/bind", controllers.BindEmail)
		authorized.POST("/profile/email/unbind", controllers.UnbindEmail)
		authorized.POST("/profile/github/unbind", controllers.UnbindGithub)

		// subscriber
		authorized.GET("/subscriber", controllers.SubscriberIndex)
		authorized.POST("/subscriber", controllers.SubscriberPost)

		// link
		authorized.GET("/link", controllers.LinkIndex)
		authorized.POST("/new_link", controllers.LinkCreate)
		authorized.POST("/link/:id/edit", controllers.LinkUpdate)
		authorized.POST("/link/:id/delete", controllers.LinkDelete)

		// comment
		authorized.POST("/comment/:id", controllers.CommentRead)
		authorized.POST("/read_all", controllers.CommentReadAll)

		// backup
		authorized.POST("/backup", controllers.BackupPost)
		authorized.POST("/restore", controllers.RestorePost)

		// mail
		authorized.POST("/new_mail", controllers.SendMail)
		authorized.POST("/new_batchmail", controllers.SendBatchMail)
	}
}

func SetVisitorRouterGroup(router *gin.Engine) {
	visitor := router.Group("/visitor")
	visitor.Use(AuthRequired())
	{
		visitor.POST("/new_comment", controllers.CommentPost)
		visitor.POST("/comment/:id/delete", controllers.CommentDelete)
	}
}

func SetJobs() {
	gocron.Every(1).Day().Do(controllers.CreateXMLSitemap)
	gocron.Every(7).Days().Do(controllers.Backup)
	gocron.Start()
}

//fixme: ?
func setTemplate(engine *gin.Engine) {

	funcMap := template.FuncMap{
		"dateFormat": helpers.DateFormat,
		"substring":  helpers.Substring,
		"isOdd":      helpers.IsOdd,
		"isEven":     helpers.IsEven,
		"truncate":   helpers.Truncate,
		"add":        helpers.Add,
		"minus":      helpers.Minus,
		"listtag":    helpers.ListTag,
	}

	engine.SetFuncMap(funcMap)
	engine.LoadHTMLGlob(filepath.Join(getCurrentDirectory(), "./views/**/*"))
}

//setSessions initializes sessions & csrf middlewares
func setSessions(router *gin.Engine) {
	config := system.GetConfiguration()
	//https://github.com/gin-gonic/contrib/tree/master/sessions
	store := sessions.NewCookieStore([]byte(config.SessionSecret))
	//7 days
	store.Options(sessions.Options{HttpOnly: true, MaxAge: 7 * 86400, Path: "/"}) //Also set Secure: true if using SSL, you should though
	router.Use(sessions.Sessions("gin-session", store))
	//https://github.com/utrack/gin-csrf
	/*router.Use(csrf.Middleware(csrf.Options{
		Secret: config.SessionSecret,
		ErrorFunc: func(c *gin.Context) {
			c.String(400, "CSRF token mismatch")
			c.Abort()
		},
	}))*/
}

//+++++++++++++ middlewares +++++++++++++++++++++++

//SharedData fills in common data, such as user info, etc...
//Put user's info into context.
func SharedData() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		if uID := session.Get(controllers.SESSION_KEY); uID != nil {
			user, err := models.GetUser(uID)
			if err == nil {
				seelog.Debugf("@@@[SharedData],put user from session into context: %s", user)
				c.Set(controllers.CONTEXT_USER_KEY, user)
			}
		}
		if system.GetConfiguration().SignupEnabled {
			c.Set("SignupEnabled", true)
		}
		c.Next()
	}
}

//AuthRequired grants access to authenticated users, requires SharedData middleware
func AdminScopeRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if user, _ := c.Get(controllers.CONTEXT_USER_KEY); user != nil {
			if u, ok := user.(*models.User); ok && u.IsAdmin {
				c.Next()
				return
			}
		}
		seelog.Warnf("User not authorized to visit %s", c.Request.RequestURI)
		c.HTML(http.StatusForbidden, "errors/error.html", gin.H{
			"message": "Forbidden!",
		})
		c.Abort()
	}
}

func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if user, _ := c.Get(controllers.CONTEXT_USER_KEY); user != nil {
			if _, ok := user.(*models.User); ok {
				c.Next()
				return
			}
		}
		seelog.Warnf("User not authorized to visit %s", c.Request.RequestURI)
		c.HTML(http.StatusForbidden, "errors/error.html", gin.H{
			"message": "Forbidden!",
		})
		c.Abort()
	}
}

func getCurrentDirectory() string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		seelog.Critical(err)
	}
	return strings.Replace(dir, "\\", "/", -1)
}

//func getCurrentDirectory() string {
//	return ""
//}
