-- 初期データ（1_InitData.sql は列の位置指定 INSERT なので元のテーブル定義のまま流す）を入れた後に形を変える。
--   isu_condition: 主キーを (jia_isu_uuid, timestamp) にして ISU ごとの時系列をクラスタ化する（ISU 単位で時刻は重複しない）
--                  condition_level = "=true" の数（0: info / 1,2: warning / 3: critical）を生成列で持ち、SQL で絞り込む
--   isu:           jia_user_id / character で引けるようにする（行に画像 BLOB があるので全件走査が重い）
ALTER TABLE `isu_condition`
  DROP PRIMARY KEY,
  DROP COLUMN `id`,
  DROP INDEX `idx_uuid_timestamp`,
  ADD COLUMN `condition_level` TINYINT AS ((LENGTH(`condition`) - LENGTH(REPLACE(`condition`, '=true', ''))) DIV 5) PERSISTENT,
  ADD PRIMARY KEY (`jia_isu_uuid`, `timestamp`);
ALTER TABLE `isu` ADD INDEX `idx_user` (`jia_user_id`), ADD INDEX `idx_character` (`character`);
