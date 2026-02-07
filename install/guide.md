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
#mainnet
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


聪网节点
====
下载satoshinet的源代码，并且编译后，得到可执行文件satoshinet。节点一般以四种形态运行，一种是引导节点，目前由协议开发团队负责维护；一种是核心节点，由各个服务提供商提供；一种是普通挖矿节点，除了挖矿，不提供任何服务；还有一种是普通节点，只同步和验证交易数据，没有任何的收入激励，但为本地应用开发提供了全网的数据索引接口。下面我们分别简单说明不同节点的配置和运行。

非挖矿节点
----
非挖矿节点，按照如下步骤运行节点
1. 将satsnet_testnet.conf拷贝到可执行文件 satoshinet 的同目录下
2. 改名为 satsnet.conf
3. 直接运行即可



挖矿节点
----
我们先提供在mainnet上质押挖矿的教程。testnet4的流程一样，只是资产是ordx:f:dogcoin，数量是1000。
挖矿节点挖矿时需要用钱包签名，需要导入stp模块，但目前stp模块还没有开源，我们提供了最新代码编译的可执行文件在./satoshinet/install/ubuntu_22.04 这个目录下。

质押：
在mainnet质押，需要将100万枚ordx:f:pearl质押到你和服务节点的通道地址上。你可以选择引导节点作为服务节点，这种时候你本身会成为核心节点，需要提供全功能服务；你也可以选择其他核心节点作为服务节点，这个时候你就是一个普通的挖矿节点，不需要提供其他服务。（目前暂时只能选择普通挖矿节点）
插件钱包提供了质押入口，你只需要选择：配置->节点配置，选择节点类型，确认你的钱包中有足够的质押资产，然后发起质押。该质押过程会将你钱包中足够数量的质押资产，转移到通道地址中。在等待交易完成的时间，可以继续做下一步，准备启动挖矿服务。

挖矿：
1. 将这个目录satoshinet/install/ubuntu_22.04的两个文件satoshinet和stpd.so下载到本地，比如 /data/satoshinet 目录下
2. 将satsnet_mainnet.conf拷贝到 /data/satoshinet 目录下
3. 改名为 satsnet.conf
4. 修改你的bitcoind的rpc用户名和密码和钱包公钥
rpcuser=your_name
rpcpass=your_password

; 可以从钱包的配置-安全-显示pubkey，将公钥复制到这个配置项
miningpubkey=your_wallet_pubkey

5. 将conf_mainnet.yaml拷贝到 /data/satoshinet 目录下，然后修改文件名为conf.yaml。打开这个文件，修改 indexer_layer1 的 host为你自建的索引器服务地址。如果在索引器在同一台电脑，不用修改。
6. 输入命令行 ./satoshinet ， 按照提示导入钱包，并且设定一个钱包密码。该密码会自动保存在 /data/satoshinet/wallet.password 中，方便调试阶段不用重复输入密码。等调试完成后，启动服务之后，可以删除这个文件，防止密码泄漏。
7. 设定密码之后，app会自动退出，现在继续输入命令行 ./satoshinet 观察挖矿服务是否顺利启动。(需要在上面的质押交易确认后再启动)
8. 如果交易已经确认，启动satoshinet后，将自动将该节点提升为挖矿节点，这个过程可能需要几分钟，在这之前，app可能会因为还不符合挖矿条件自动退出，需要多启动几次。
9. 最后，如果一切顺利，日志会打印 “Start pos miner.”，节点正式进入挖矿状态。
10. 按 ctrl-c （或者kill -2 satoshinet）退出app，重新用下面命令行启动节点，进入服务状态：
nohup ./satoshinet > ./nohup.log 2>&1 &
11. 这个时候可以删除wallet.password文件 （下次重启，需要先将创建一个同样的文件，并且将密码输入其中）



区块浏览器
---
https://mempool.sat20.org
