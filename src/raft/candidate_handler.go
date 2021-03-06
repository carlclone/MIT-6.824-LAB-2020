package raft

func (rf *Raft) candElectTimeoutHandler() {
	rf.becomeCandidate()
}

// cand 收到响应投票,票数++ , 判断是否 -> leader
func (rf *Raft) candRespRVHandler(request VoteRequest) {
	reply := request.reply

	if reply.VoteGranted {
		rf.print(LOG_ALL, "收到支持投票，来自%v", reply.From)
		rf.voteCount++
		if rf.voteCount > rf.peerCount/2 {
			rf.becomeLeader()
		}
	}
}

//cand收到投票 , 公共处理
func (rf *Raft) candReqsRVHandler(request VoteRequest) {
	rf.finishReqsRVHandle <- true
}

//cand收到心跳 , 只需要按照公共处理
func (rf *Raft) candReqsAEHandler(request AppendEntriesRequest) {
	rf.becomeFollower(request.args.Term)
	rf.finishReqsAEHandle <- true
}
