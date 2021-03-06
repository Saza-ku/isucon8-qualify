## TIPS

### 立ち回り
- ボトルネックを攻める人、エスパーで遅そうなところを攻める人、とかで分かれる
- やることなくなったらマニュアルとレギュレーション読み直す
- マニュアルにあるヒントには素直に従ってみる
- 軽率にペアプロをする

### 改善策
- 重くてどうにもならない処理は複数台で分ける
- 外部 API 呼び出しがあったらとりあえず注意
  - どれくらい時間かかるか調べる (pprof では計測できない(IO は取れないため))
  - できれば呼び出さなくて済むようにする
- CPU リソースが余るときは、使い切れるようにするための設定がある (マニュアルを読め)
- OR は UNION で書き換えることでインデックスが効きやすくできる
- 無駄な SELECT FOR UPDATE や、トランザクション、ロックなどは削る
- MySQL 5.7 だと ASC, DESC 混じりは効かない
- N+1 はとりあえず潰しとく
- LIMIT 使えないか考える
