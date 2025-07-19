聪网节点安装说明 (测试网)
====


聪网节点依赖比特币主网节点和索引器，在运行聪网节点之前，需要安装运行比特币节点和索引器。
我们在实践中使用了Bitcoin Core v28.0.0 版本。其他版本应该也可以，但我们没有测试过。
操作系统方面，我们主要使用 Ubuntu 22.04.4 LTS 运行各种服务，mac系统也可以正常使用，因为我们开发环境主要是mac系统。
我们不能保证所有服务在windows系统中正常运行。


安装和运行Bitcoin Core
----
访问 https://bitcoincore.org/en/download/
选择适合你系统的版本，推荐使用跟我们一样的版本。然后按照Bitcoin Core的指引安装并运行。
运行bitcoind同步数据可能需要几天时间。在同步数据的同时，可以开始聪网节点的编译工作。


下面是一个简单的bitcoind配置文件，支持主网和testnet4，启动时可以指定其中一个配置。

#假定文件名称为：bitcoin.conf
daemon=1
server=1
maxmempool=1024
txindex=1
coinstatsindex=1

[main]
addnode=1.116.110.123:8333
addnode=123.60.213.192:8333
addnode=27.148.206.140:8333
addnode=182.100.67.50:8333
addnode=114.231.12.9:8333
addnode=222.186.20.60:8333
addnode=120.79.71.72:8333
addnode=47.99.90.156:8333

zmqpubrawblock=tcp://0.0.0.0:38332
zmqpubrawtx=tcp://0.0.0.0:38333
rpcuser=your_name
rpcpassword=your_password
rpcallowip=0.0.0.0/0
rpcbind=0.0.0.0:8332

[testnet4]
zmqpubrawblock=tcp://0.0.0.0:58332
zmqpubrawtx=tcp://0.0.0.0:58333
rpcuser=your_name
rpcpassword=your_password
rpcallowip=0.0.0.0/0
rpcbind=0.0.0.0:28332


可以指定以某个配置运行bitcoind
bitcoind -chain=main -conf=/data/bitcoin/bitcoin.conf -datadir=/data/bitcoin/main





编译和运行聪网节点
----
我们目前已经开源了绝大多数的代码，只有聪穿越协议，因为还没成熟，暂时还没有开放。
源代码：https://github.com/sat20-labs

很多代码库修改非常频繁，需要将多个代码clone到本地，并且放到同一个工程目录中
索引器：https://github.com/sat20-labs/indexer

workspace
 |- bitcoin 
 |- indexer    https://github.com/sat20-labs/indexer
 |- satoshinet  https://github.com/sat20-labs/satoshinet
 |- sat20wallet  https://github.com/sat20-labs/sat20wallet
 |- transcend (暂时未开源，提供so供节点调用)


请先编译好indexer，并且在bitcoind节点同步到最新区块后，运行indexer，跑全网数据。注意，indexer跑全网数据时间比较长，一般要5-7天，根据机器性能而定。


(等版本稳定，我们会提供适合Ubuntu 22.04.4 LTS运行的可执行文件供大家下载，这样避免编译代码的麻烦)

索引器的配置文件（假定配置文件名字：indexer_mainnet.yaml）
# mainnet
chain: mainnet
db:
  path: ./db/mainnet
share_rpc:
  bitcoin:
    host: 127.0.0.1
    port: 8332
    user: your_name
    password: your_password
log:
  level: info # default info
  path: ./log/mainnet # default log
basic_index:
  max_index_height: 0 # default 0, set not 0 to stop at this height
  period_flush_to_db: 100 # default 100
rpc_service:
  addr: 0.0.0.0:8005
  proxy: btc/mainnet
  log_path: log/mainnet

其中bitcoin的配置，是你在bitcoind中对应的配置。假定bitcoind运行在同一台机器上。

然后执行命令，运行indexer开始跑数据：
 ./indexer -env ./indexer_mainnet.yaml

重点注意：indexer跑数据过程，如果异常中断，会导致数据不可用，只能从头跑。最好的方式，是先设定一个高度，比如ordinals协议启用的高度767430（通过设置max_index_height），作为第一个高度，跑到该高度，先备份数据库，然后继续跑。可以备份数据库，比如再备份一个900000高度的数据。

备份数据时，需要先停止indexer（kill -2 indexer），将./db/mainnet 这个目录直接拷贝一份就可以了。

至此，等待索引器同步到最新高度后，准备工作就基本完成了。



非挖矿节点
----
非挖矿节点，按照如下步骤运行节点
1. 将satsnet_testnet.conf拷贝到可执行文件 satoshinet 的同目录下
2. 改名为 satsnet.confg
3. 直接运行即可



挖矿节点
----
挖矿节点需要stp模块的支持，需要一个特别的 satoshinet 版本，该版本集成了stp模块。（因为stp模块还没有开源）

1. 将satsnet_miner_testnet.conf拷贝到可执行文件 satoshinet 的同目录下
2. 改名为 satsnet.confg
3. 修改satsnet.confg中的validatorid，改成一个随机的int64值，比如节点的公网ip地址的int64格式
4. 将conf.yaml拷贝到可执行文件 satoshinet 的同目录下
5. 运行 ./satoshinet 根据提示创建钱包，备份助记词和密码
6. 


区块浏览器
---
https://mempool.test.sat20.org
