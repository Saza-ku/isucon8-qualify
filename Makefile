
.PHONY: build
build:
	cd ./webapp/go; \
	go build -o torb app.go; \
	sudo systemctl restart torb.go.service;

.PHONY: pprof
pprof:
	go tool pprof -http=0.0.0.0:8080 /home/isucon/webapp/go/isucondition http://localhost:6060/debug/pprof/profile


MYSQL_HOST="127.0.0.1"
MYSQL_PORT=3306
MYSQL_USER=isucon
MYSQL_DBNAME=torb
MYSQL_PASS=isucon

MYSQL=mysql -h$(MYSQL_HOST) -P$(MYSQL_PORT) -u$(MYSQL_USER) -p$(MYSQL_PASS) $(MYSQL_DBNAME)
SLOW_LOG=/var/log/mariadb/slow.log

# slow-wuery-logを取る設定にする
# DBを再起動すると設定はリセットされる
.PHONY: slow-on
slow-on:
	-sudo rm $(SLOW_LOG)
	sudo systemctl restart mariadb
	$(MYSQL) -e "set global slow_query_log_file = '$(SLOW_LOG)'; set global long_query_time = 0.001; set global slow_query_log = 1;"

.PHONY: slow-off
slow-off:
	$(MYSQL) -e "set global slow_query_log = OFF;"

# mysqldumpslowを使ってslow wuery logを出力
# オプションは合計時間ソート
.PHONY: slow-show
slow-show:
	sudo mysqldumpslow -s t $(SLOW_LOG) | head -n 20
.PHONY: slow-detail
slow-detail:
	sudo cat /var/log/mariadb/slow.log | pt-query-digest --limit 8

# alp
.PHONY: alp
alp:
	alp ltsv -c alp.yaml

.PHONY: alpreset
alp-reset:
	-sudo rm /var/log/h2o/access.log; \
	sudo systemctl restart h2o;
	
.PHONY: prebench
prebench: build alp-reset slow-on

.PHONY: applog
applog: 
	sudo journalctl -u torb.go --no-pager | tail -n 40

