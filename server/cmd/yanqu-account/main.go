// 颜趣本地账号管理：在个播新机本地运行，创建/重置用户名密码账号并加入工作区。
//
// 用法（读取与后端相同的 DATABASE_URL）：
//   yanqu-account add <用户名> <显示名> [--role owner|admin|member] [--workspace yanqu] [--link-email 已有邮箱]
//   yanqu-account passwd <用户名>
//   yanqu-account disable|enable <用户名>
//   yanqu-account rename <旧用户名> <新用户名>
//   yanqu-account list
// 密码从标准输入读取一行，不出现在命令行参数里。
package main

import (
	"bufio"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.@+-]{1,63}$`)

func hash(pw string) (string, error) {
	if len(pw) < 8 {
		return "", errors.New("密码至少 8 位")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, 210000, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", 210000, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func readPassword() string {
	s := bufio.NewScanner(os.Stdin)
	if s.Scan() {
		return strings.TrimRight(s.Text(), "\r\n")
	}
	return ""
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }

func main() {
	if len(os.Args) < 2 {
		die("用法：yanqu-account add|passwd|disable|enable|list ...")
	}
	ctx := context.Background()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		die("需要 DATABASE_URL")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		die("连接数据库失败：%v", err)
	}
	defer pool.Close()
	cmd := os.Args[1]
	switch cmd {
	case "list":
		rows, err := pool.Query(ctx, `SELECT l.username, u.name, u.email, l.disabled,
		  COALESCE((SELECT string_agg(w.slug||':'||m.role, ',') FROM member m JOIN workspace w ON w.id=m.workspace_id WHERE m.user_id=u.id),'')
		  FROM yanqu_local_login l JOIN "user" u ON u.id=l.user_id ORDER BY l.username`)
		if err != nil {
			die("%v", err)
		}
		for rows.Next() {
			var un, name, email, ws string
			var dis bool
			_ = rows.Scan(&un, &name, &email, &dis, &ws)
			fmt.Printf("%s\t%s\t%s\tdisabled=%v\t%s\n", un, name, email, dis, ws)
		}
	case "add":
		fs := flag.NewFlagSet("add", flag.ExitOnError)
		role := fs.String("role", "member", "owner|admin|member")
		wsSlug := fs.String("workspace", "yanqu", "工作区 slug")
		linkEmail := fs.String("link-email", "", "绑定已有用户邮箱（不新建用户）")
		if len(os.Args) < 4 {
			die("用法：yanqu-account add <用户名> <显示名> [--role] [--workspace] [--link-email]")
		}
		un, display := strings.ToLower(os.Args[2]), os.Args[3]
		_ = fs.Parse(os.Args[4:])
		if !usernameRe.MatchString(un) {
			die("用户名只能用小写字母、数字及 . _ - @ +，2-64 位")
		}
		if *role != "owner" && *role != "admin" && *role != "member" {
			die("role 只能是 owner/admin/member")
		}
		h, err := hash(readPassword())
		if err != nil {
			die("%v", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			die("%v", err)
		}
		defer tx.Rollback(ctx)
		var userID string
		if *linkEmail != "" {
			if err := tx.QueryRow(ctx, `SELECT id::text FROM "user" WHERE email=$1`, strings.ToLower(*linkEmail)).Scan(&userID); err != nil {
				die("找不到邮箱 %s 对应的用户", *linkEmail)
			}
		} else {
			// 上游要求 email 唯一且非空：用户名账号使用不可投递的本地占位地址。
			email := un + "@yanqu.local"
			err := tx.QueryRow(ctx, `INSERT INTO "user"(name,email) VALUES($1,$2) RETURNING id::text`, display, email).Scan(&userID)
			if err != nil {
				die("创建用户失败：%v", err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO yanqu_local_login(username,user_id,password_hash) VALUES($1,$2,$3)`, un, userID, h); err != nil {
			die("创建登录账号失败（用户名可能已存在）：%v", err)
		}
		var wsID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM workspace WHERE slug=$1`, *wsSlug).Scan(&wsID); err != nil {
			die("找不到工作区 %s", *wsSlug)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT (workspace_id,user_id) DO NOTHING`, wsID, userID, *role); err != nil {
			die("加入工作区失败：%v", err)
		}
		// 新建账号直接视为已完成引导，避免进入引导页。
		_, _ = tx.Exec(ctx, `UPDATE "user" SET onboarded_at = COALESCE(onboarded_at, now()) WHERE id=$1`, userID)
		if err := tx.Commit(ctx); err != nil {
			die("%v", err)
		}
		fmt.Printf("已创建：%s（%s），工作区 %s 角色 %s\n", un, display, *wsSlug, *role)
	case "passwd":
		if len(os.Args) < 3 {
			die("用法：yanqu-account passwd <用户名>")
		}
		h, err := hash(readPassword())
		if err != nil {
			die("%v", err)
		}
		tag, err := pool.Exec(ctx, `UPDATE yanqu_local_login SET password_hash=$2, failed_attempts=0, locked_until=NULL, updated_at=now() WHERE username=$1`, strings.ToLower(os.Args[2]), h)
		if err != nil || tag.RowsAffected() != 1 {
			die("找不到用户名 %s", os.Args[2])
		}
		fmt.Println("密码已重置")
	case "rename":
		if len(os.Args) < 4 {
			die("用法：yanqu-account rename <旧用户名> <新用户名>")
		}
		nu := strings.ToLower(strings.TrimSpace(os.Args[3]))
		if !usernameRe.MatchString(nu) {
			die("新用户名不合法")
		}
		tag, err := pool.Exec(ctx, `UPDATE yanqu_local_login SET username=$2, updated_at=now() WHERE username=$1`, strings.ToLower(os.Args[2]), nu)
		if err != nil || tag.RowsAffected() != 1 {
			die("改名失败（旧用户名不存在或新用户名已被占用）：%v", err)
		}
		fmt.Println("用户名已修改为", nu)
	case "disable", "enable":
		if len(os.Args) < 3 {
			die("用法：yanqu-account %s <用户名>", cmd)
		}
		tag, err := pool.Exec(ctx, `UPDATE yanqu_local_login SET disabled=$2, updated_at=now() WHERE username=$1`, strings.ToLower(os.Args[2]), cmd == "disable")
		if err != nil || tag.RowsAffected() != 1 {
			die("找不到用户名 %s", os.Args[2])
		}
		fmt.Println("已更新")
	default:
		die("未知命令 %s", cmd)
	}
	_ = pgx.ErrNoRows
}
