package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"bytes"
	"labgob"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)
import "../labrpc"

// import "bytes"
// import "../labgob"

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

const (
	ROLE_LEADER    = 1
	ROLE_CANDIDATE = 2
	ROLE_FOLLOWER  = 3
)

type Entry struct {
	Command interface{}
	/* 任期号用来检测多个日志副本之间的不一致情况
	 * Leader 在特定的任期号内的一个日志索引处最多创建一个日志条目，同时日志条目在日志中的位置也从来不会改变。该点保证了上面的第一条特性。
	 */
	Term  int //
	Index int
}

type Request struct {
	args  interface{}
	reply interface{}
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	role int //角色

	//持久化的
	currentTerm int //理解为logical clock , 当前的任期
	voteFor     int //投票对象
	log         []Entry

	//volatile
	commitIndex int //当前已提交的最后一个index
	lastApplied int //最后一个通知客户端完成复制的index

	//leader属性
	nextIndex  []int //下一个要发送给peers的entry的index , 用于定位 peer和leader日志不一致的范围
	matchIndex []int //peer们最后一个确认复制的index ,用于apply

	receiveRequestVote        chan Request
	requestVoteHandleFinished chan bool

	receiveAppendEntries        chan Request
	appendEntriesHandleFinished chan bool

	startNewElection   chan bool
	concurrentSendVote chan bool
}

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
}

type RequestVoteReply struct {
}

type AppendEntriesArgs struct {
}

type AppendEntriesReply struct {
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.receiveRequestVote <- Request{args, reply}
	<-rf.requestVoteHandleFinished
	return

}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.receiveAppendEntries <- Request{args, reply}
	<-rf.appendEntriesHandleFinished
	return
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	return false
}

// Make() must return quickly, so it should start goroutines for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	//主事件循环线程
	go func() {
		for {
			switch rf.role {
			case ROLE_LEADER:
				select {
				//处理心跳包
				case <-rf.receiveAppendEntries:
				//处理投票
				case <-rf.receiveRequestVote:
				}
			case ROLE_CANDIDATE:
				select {
				case <-rf.startNewElection:
					rf.concurrentSendVote <- true
				//处理心跳包
				case <-rf.receiveAppendEntries:
				//处理投票
				case <-rf.receiveRequestVote:
				//选举超时,新一轮选举
				case <-time.After(rf.electionTimeOut()):
					rf.startNewElection <- true
				}
			case ROLE_FOLLOWER:
				select {
				//处理心跳包
				case <-rf.receiveAppendEntries:
				//处理投票
				case <-rf.receiveRequestVote:
				//选举超时,开始新一轮选举
				case <-time.After(rf.electionTimeOut()):
					rf.role = ROLE_CANDIDATE
					rf.startNewElection <- true
				}
			}
		}
	}()
	//并发发送网络请求线程 心跳包 , 请求投票 , follower不发送任何请求
	go func() {
		for {
			switch rf.role {
			case ROLE_LEADER:
			case ROLE_CANDIDATE:
				select {
				case <-rf.concurrentSendVote:

					///////////////////////////////////
					for i, _ := range rf.peers {
						if i == rf.me {
							continue
						}
						go func(i int) {
							args := &RequestVoteArgs{
								Term:        rf.currentTerm,
								CandidateId: rf.me,
							}
							reply := &RequestVoteReply{}
							rf.sendRequestVote(i, args, reply)
						}(i)
					}
					///////////////////////////////////

				}
			}
		}
	}()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	return rf
}

func (rf *Raft) electionTimeOut() time.Duration {
	return time.Duration((rand.Int63())%500+200) * time.Millisecond
}

// ----------------------- stable ---------------------------- //

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	return rf.currentTerm, rf.role == ROLE_LEADER
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.voteFor)
	e.Encode(rf.currentTerm)
	e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var votedFor int
	var currentTerm int
	var log []Entry
	if d.Decode(&votedFor) != nil ||
		d.Decode(&currentTerm) != nil ||
		d.Decode(&log) != nil {
	} else {
		rf.voteFor = votedFor
		rf.currentTerm = currentTerm
		rf.log = log
	}
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	index := -1
	term := -1
	isLeader := rf.isLeader()

	if isLeader {
		index = rf.appendCommand(command)
		term = rf.currentTerm
		//rf.print(LOG_REPLICA_1, "客户端发起command: isLeader:%v index:%v,term:%v", isLeader, index, term)
	}

	return index, term, isLeader
}

func (rf *Raft) isLeader() bool {
	return rf.role == ROLE_LEADER
}

func (rf *Raft) appendCommand(command interface{}) int {
	replicatedIndex := len(rf.log) - 1
	nextIndex := replicatedIndex + 1
	rf.log = append(rf.log, Entry{command, rf.currentTerm, nextIndex})
	rf.persist()
	return nextIndex
}
