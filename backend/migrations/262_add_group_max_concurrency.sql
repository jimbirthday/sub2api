-- Add per-group total concurrency limit.
-- max_concurrency: 分组整体并发上限（0 = 不限制）。
-- 当 >0 时，该分组下所有用户/账号共享这一组级并发槽位（concurrency:group:{group_id}），
-- 超过即进入等待队列或返回 429，而不是仅靠各账号/各用户独立上限。
ALTER TABLE groups ADD COLUMN IF NOT EXISTS max_concurrency integer NOT NULL DEFAULT 0;

COMMENT ON COLUMN groups.max_concurrency IS '分组整体并发上限；0 表示不限制；>0 时该分组所有请求共享此并发闸门。';
