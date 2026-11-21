package main

import (
	"log"
	"os"
	"strings"
	"time"

	"bcn-noti-service/config"
	"bcn-noti-service/handler"
	"bcn-noti-service/provider"
	"bcn-noti-service/zalo_bot"
)

func main() {
	log.SetOutput(os.Stdout)
	_ = config.LoadEnv(".env")

	discordWebhook := os.Getenv("DISCORD_ADMIN_GET_QR_PRIVATE_WEBHOOK")
	cookieMgr := zalo_bot.NewCookieManager("zalo_session.json", discordWebhook)
	cookieMgr.StartHealthCheck(5 * time.Minute)

	groupID := os.Getenv("BCN_ZALO_GROUP_ID")
	if groupID == "" {
		groupID = "4025169182418597977"
	}
	zaloProv := provider.NewZaloProvider(cookieMgr, "", groupID)

	mentorStr := os.Getenv("MENTOR_ZALO_UIDS")
	var mentorList []string
	if mentorStr != "" {
		for _, uid := range strings.Split(mentorStr, ",") {
			if trimmed := strings.TrimSpace(uid); trimmed != "" {
				mentorList = append(mentorList, trimmed)
			}
		}
	}
	if len(mentorList) == 0 {
		mentorList = []string{"111111", "222222"}
	}

	zaloCfg := handler.ZaloConfig{
		AppID:         os.Getenv("ZALO_APP_ID"),
		AppSecret:     os.Getenv("ZALO_APP_SECRET"),
		RedirectURI:   "http://localhost:8080/auth/zalo/callback",
		MentorUIDs:    mentorList,
		ZaloProvider:  zaloProv,
		CookieManager: cookieMgr,
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server listening on :%s\n", port)
	if err := handler.StartServer("web", ":"+port, zaloCfg); err != nil {
		log.Fatal(err)
	}
}
