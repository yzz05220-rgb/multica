-- 颜趣本地补丁：用户名允许邮箱格式（只作登录名，不发信）。
ALTER TABLE yanqu_local_login DROP CONSTRAINT IF EXISTS yanqu_local_login_username_check;
ALTER TABLE yanqu_local_login ADD CONSTRAINT yanqu_local_login_username_check
  CHECK (username ~ '^[a-z0-9][a-z0-9_.@+-]{1,63}$');
