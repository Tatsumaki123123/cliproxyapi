// 响应时间测试脚本
// 使用方法：
// 1. 运行此脚本：node timing-test.js
// 2. 记录开始时间
// 3. 向 Claude 发送消息
// 4. Claude 回复后，再次运行脚本查看时间差

const now = Date.now();
const timestamp = new Date().toISOString();

console.log('='.repeat(50));
console.log('当前时间:', timestamp);
console.log('Unix 时间戳:', now);
console.log('='.repeat(50));

// 如果你想计算两次运行之间的时间差，可以手动记录
// 例如：第一次运行得到 1713340800000
//      第二次运行得到 1713340805000
//      时间差 = 5000ms = 5秒
