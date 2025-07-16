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



编译和运行聪网节点
----
我们目前已经开源了绝大多数的代码，只有聪穿越协议，因为还没成熟，暂时还没有开放。
源代码：https://github.com/sat20-labs

很多代码库修改非常频繁，需要将多个代码clone到本地，并且放到同一个工程目录中
索引器：https://github.com/sat20-labs/indexer

workspace
 |- indexer    https://github.com/sat20-labs/indexer
 |- satoshinet  https://github.com/sat20-labs/satoshinet
 |- sat20wallet  https://github.com/sat20-labs/sat20wallet
 |- transcend (暂时未开源，提供so供节点调用)


请先编译好indexer，并且在bitcoind节点同步到最新区块后，运行indexer，跑全网数据。注意，indexer跑全网数据时间比较长，一般要5-7天，根据机器性能而定。


(等版本稳定，我们会提供适合Ubuntu 22.04.4 LTS运行的可执行文件供大家下载，这样避免编译代码的麻烦)


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
