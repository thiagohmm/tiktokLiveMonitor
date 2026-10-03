// import-identities validates/imports a password-free export into local PostgreSQL.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/mail"
)

func main() {
	file := flag.String("file", "", "JSON de identidades sem senhas")
	apply := flag.Bool("apply", false, "aplicar importação (padrão: validação com rollback)")
	links := flag.String("activation-links-file", "", "arquivo privado para links de ativação")
	send := flag.Bool("send-activation", false, "enviar links por SMTP/Resend após importar")
	flag.Parse()
	if *file == "" {
		log.Fatal("informe --file")
	}
	if (*links != "" || *send) && !*apply {
		log.Fatal("links exigem --apply")
	}
	f, err := os.Open(*file)
	if err != nil {
		log.Fatal(err)
	}
	defer closeFile(f)
	var payload struct {
		Users []auth.ImportAccount `json:"users"`
	}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil {
		log.Fatal(err)
	}
	db, err := database.OpenFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("database close: %v", err)
		}
	}()
	store := auth.NewStore(db.SQLDB())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := store.ImportUsers(ctx, payload.Users, *apply)
	if err != nil {
		log.Fatal(err)
	}
	if *links != "" || *send {
		site := auth.LoadConfigFromEnv().SiteURL
		if site == "" {
			log.Fatal("SITE_URL obrigatório para ativação")
		}
		mailer := mail.NewMailer(mail.LoadConfigFromEnv())
		if *send && !mailer.Enabled() {
			log.Fatal("envio de e-mail não configurado")
		}
		var out *os.File
		if *links != "" {
			out, err = os.OpenFile(*links, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				log.Fatal(err)
			}
			defer closeFile(out)
		}
		for _, account := range payload.Users {
			var hasPassword bool
			if err = store.DB.QueryRowContext(ctx, `SELECT password_hash IS NOT NULL FROM users WHERE id=$1`, account.ID).Scan(&hasPassword); err != nil {
				log.Fatal(err)
			}
			if hasPassword {
				continue
			}
			link, err := store.GenerateActionLink(account.ID, "activation", site+"/reset-password.html", 7*24*time.Hour)
			if err != nil {
				log.Fatal(err)
			}
			if out != nil {
				if _, err = fmt.Fprintf(out, "%s\t%s\n", account.Email, link); err != nil {
					log.Fatal(err)
				}
			}
			if *send {
				if err = mailer.SendPasswordReset(account.Email, link); err != nil {
					log.Printf("ativação não enviada para ID %s: %v", account.ID, err)
				}
			}
		}
	}
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		log.Fatal(err)
	}
}

func closeFile(file *os.File) {
	if err := file.Close(); err != nil {
		log.Printf("file close: %v", err)
	}
}
