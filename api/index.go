package handler

import (
	"net/http"
	"os"
	"strings"
	"sync"

	"bcn-noti-service/config"
	bcnHandler "bcn-noti-service/handler"
	"bcn-noti-service/provider"
	"bcn-noti-service/zalo_bot"
)

var once sync.Once

func initServerless() {
	_ = config.LoadEnv(".env")

	discordWebhook := os.Getenv("DISCORD_ADMIN_GET_QR_PRIVATE_WEBHOOK")
	if discordWebhook == "" {
		discordWebhook = "https://discord.com/api/webhooks/link_nhan_canh_bao_admin"
	}
	cookieMgr := zalo_bot.NewCookieManager("zalo_session.json", discordWebhook)

	groupID := os.Getenv("ZALO_GROUP_ID")
	if groupID == "" {
		groupID = "4025169182418597977"
	}
	zaloProv := provider.NewZaloProvider(cookieMgr, "", groupID)

	mentorStr := os.Getenv("MENTOR_ZALO_UIDS")
	var mentorList []string
	if mentorStr != "" {
		for _, uid := range strings.Split(mentorStr, ",") {
			trimmed := strings.TrimSpace(uid)
			if trimmed != "" {
				mentorList = append(mentorList, trimmed)
			}
		}
	}
	if len(mentorList) == 0 {
		mentorList = []string{"111111", "222222"}
	}

	zaloCfg := bcnHandler.ZaloConfig{
		AppID:         os.Getenv("ZALO_APP_ID"),
		AppSecret:     os.Getenv("ZALO_APP_SECRET"),
		RedirectURI:   os.Getenv("ZALO_REDIRECT_URI"),
		MentorUIDs:    mentorList,
		ZaloProvider:  zaloProv,
		CookieManager: cookieMgr,
	}

	bcnHandler.SetupZaloAuthRoutes(zaloCfg)
	bcnHandler.SetupPostAndBotRoutes(zaloCfg, cookieMgr)
	bcnHandler.SetupTestRoutes(zaloCfg, cookieMgr)
	bcnHandler.SetupQRRoutes(cookieMgr)
}

func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(initServerless)
	http.DefaultServeMux.ServeHTTP(w, r)
}
