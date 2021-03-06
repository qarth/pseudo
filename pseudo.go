// pseudo.go implements pseudo3.23.
// MIT license in accompanying LICENSE file.

// NOTES:
// 1. Input is from stdin - c_src#readDimacsFileCreateList.
//    This looks a little cludgy.  main()/Testxxx() should pass in a file
//    handle that may be os.Stdin.
// 2. In RecoverFlow() use gap value based on PseudoCtx.Lowestlabel value.
// 3. All timing/profiling is out in main()/Testxxx - so don't include in this package.
// 4. main() in C source code is really just a test ... implement in pseudo_test.go.

// Package pseudo is a port of pseudo3.23 from C to Go.
//
// The easiest way to use this package is to call pseudo.Run(<input file>) after setting
// the runtime context options, if desired. However it is also possible to call the
// individual processing functions - ReadDimacsFile, SimpleInitialization, FlowPhaseOne,
// RecoverFlow, Results - sequentially.
package pseudo

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
)

// global variables
var lowestStrongLabel uint
var highestStrongLabel uint
var adjacencyList []*node
var strongRoots []*root
var arcList []*arc
var labelCount []uint
var numNodes, numArcs, source, sink uint

// local context

// Context provides optional switches that can be set using Config.
type Context struct {
	LowestLabel bool
	FifoBucket  bool
	// Stats       bool // always collect stats, reporting just requires call to StatsJSON
}

// PseudoCtx can also be set from a file using Config, but
// that requires importing github.com/clbanning/checkjson.
var PseudoCtx Context

// ConfigJSON returns the runtime context settings as a JSON object.
func ConfigJSON() string {
	j, _ := json.Marshal(PseudoCtx)
	return string(j)
}

// statistics
type statistics struct {
	NumPushes   uint `json:"numPushes"`
	NumMergers  uint `json:"numMergers"`
	NumRelabels uint `json:"numRelabels"`
	NumGaps     uint `json:"numGaps"`
	NumArcScans uint `json:"numArcScans"`
}

var stats statistics

// StatsJSON returns the runtime stats as a JSON object.
func StatsJSON() string {
	j, _ := json.Marshal(stats)
	return string(j)
}

// ==================== the arc object
type arc struct {
	from      *node
	to        *node
	flow      uint
	capacity  uint
	direction uint
}

// (*arc) pushUpward
// static inline void
func (a *arc) pushUpward(child *node, parent *node, resCap uint) {

	stats.NumPushes++
	if resCap >= child.excess {
		parent.excess += child.excess
		a.flow += child.excess
		child.excess = 0
		return
	}

	a.direction = 0
	parent.excess += resCap // int and uint
	child.excess -= resCap  // int and uint
	a.flow = a.capacity
	parent.outOfTree[parent.numberOutOfTree] = a
	parent.numberOutOfTree++
	parent.breakRelationship(child)
	if PseudoCtx.LowestLabel {
		lowestStrongLabel = child.label
	}

	child.addToStrongBucket(strongRoots[child.label])
}

// (*arc) pushDownward
//static inline void
func (a *arc) pushDownward(child *node, parent *node, flow uint) {

	stats.NumPushes++

	if flow >= child.excess {
		parent.excess += child.excess
		a.flow = child.excess
		child.excess = 0
	}

	a.direction = 1
	child.excess -= flow
	parent.excess += flow
	a.flow = 0
	parent.outOfTree[parent.numberOutOfTree] = a
	parent.numberOutOfTree++
	parent.breakRelationship(child)
	if PseudoCtx.LowestLabel {
		lowestStrongLabel = child.label
	}

	child.addToStrongBucket(strongRoots[child.label])
}

//Initialize a new arc value.
//in-lined
//func newArc() *arc {
//	return &arc{direction: 1}
//}

// ==================== the node object
type node struct {
	visited         uint
	numAdjacent     uint
	number          uint
	label           uint
	excess          uint
	parent          *node
	childList       *node
	nextScan        *node
	numberOutOfTree uint
	outOfTree       []*arc // was **Arc in C, looking at CreateOutOfTree, we're dealing with a pool of Arc's
	nextArc         uint
	arcToParent     *arc
	next            *node
}

// #ifdef LOWEST_LABEL
// static Node *
// getLowestStrongRoot (void)
func getLowestStrongRoot() *node {
	var i uint
	var strongRoot *node

	if lowestStrongLabel == 0 {
		for strongRoots[0].start != nil {
			strongRoot = strongRoots[0].start
			strongRoots[0].start = strongRoot.next
			strongRoot.next = nil
			strongRoot.label = uint(1)

			labelCount[0]--
			labelCount[1]++
			stats.NumRelabels++

			strongRoot.addToStrongBucket(strongRoots[strongRoot.label])
		}
		lowestStrongLabel = 1
	}

	for i = lowestStrongLabel; i < numNodes; i++ {
		if strongRoots[i].start != nil {
			lowestStrongLabel = i

			if labelCount[i-1] == 0 {
				stats.NumGaps++
				return nil
			}

			strongRoot = strongRoots[i].start
			strongRoots[i].start = strongRoot.next
			strongRoot.next = nil
			return strongRoot
		}
	}

	lowestStrongLabel = numNodes
	return nil
}

// static Node *
// getHighestStrongRoot (void)
func getHighestStrongRoot() *node {
	var i uint
	var strongRoot *node

	for i = highestStrongLabel; i > 0; i-- {
		if strongRoots[i].start != nil {
			highestStrongLabel = i

			if labelCount[i-1] > 0 {
				strongRoot = strongRoots[i].start
				strongRoots[i].start = strongRoot.next
				strongRoot.next = nil
				return strongRoot
			}

			for strongRoots[i].start != nil {
				stats.NumGaps++
				strongRoot = strongRoots[i].start
				strongRoots[i].start = strongRoot.next
				strongRoot.liftAll()
			}
		}
	}

	if strongRoots[0].start != nil {
		return nil
	}

	for strongRoots[0].start != nil {
		strongRoot = strongRoots[0].start
		strongRoots[0].start = strongRoot.next
		strongRoot.label = 1

		labelCount[0]--
		labelCount[1]++
		stats.NumRelabels++

		strongRoot.addToStrongBucket(strongRoots[strongRoot.label])
	}

	highestStrongLabel = 1

	strongRoot = strongRoots[1].start
	strongRoots[1].start = strongRoot.next
	strongRoot.next = nil

	return strongRoot
}

// (*node) createOutOfTree allocates arc's for adjacent nodes.
func (n *node) createOutOfTree() {
	n.outOfTree = make([]*arc, n.numAdjacent) // OK if '0' are allocated
}

// (*node) addOutOfTreenode
func (n *node) addOutOfTreeNode(out *arc) {
	n.outOfTree[n.numberOutOfTree] = out
	n.numberOutOfTree++
}

// (*node) processRoot. 'n' is 'strongRoot' in C source
func (n *node) processRoot() {
	var temp, weakNode *node
	var out *arc
	strongNode := n
	n.nextScan = n.childList

	if out, weakNode = n.findWeakNode(); out != nil {
		weakNode.merge(n, out)
		n.pushExcess()
		return
	}

	n.checkChildren()

	for strongNode != nil {
		for strongNode.nextScan != nil {
			temp = strongNode.nextScan
			strongNode.nextScan = strongNode.nextScan.next
			strongNode = temp
			strongNode.nextScan = strongNode.childList

			if out, weakNode = strongNode.findWeakNode(); out != nil {
				weakNode.merge(strongNode, out)
				n.pushExcess()
				return
			}

			strongNode.checkChildren()
		}

		if strongNode = strongNode.parent; strongNode != nil {
			strongNode.checkChildren()
		}
	}

	n.addToStrongBucket(strongRoots[n.label])

	if !PseudoCtx.LowestLabel {
		highestStrongLabel++
	}
}

// static void
// merge (Node *parent, Node *child, Arc *newArc)
// (*node) merge. 'n' is 'parent' in C source.
func (n *node) merge(child *node, newArc *arc) {
	var oldArc *arc
	var oldParent *node
	current := child
	newParent := n

	stats.NumMergers++ // unlike C source always calc stats

	for current != nil {
		oldArc = current.arcToParent
		current.arcToParent = newArc
		oldParent = current.parent
		oldParent.breakRelationship(current)
		newParent.addRelationship(current)

		newParent = current
		current = oldParent
		newArc = oldArc
		newArc.direction = 1 - newArc.direction
	}

	current.arcToParent = newArc
	newParent.addRelationship(current)
}

// static void
// pushExcess (Node *strongRoot)
// (*node) pushExcess. 'n' is 'strongRoot' in C source.
func (n *node) pushExcess() {
	var current, parent *node
	var arcToParent *arc
	prevEx := uint(1)

	for current = n; current.excess > 0 && current.parent != nil; current = parent {
		parent = current.parent
		prevEx = parent.excess

		arcToParent = current.arcToParent

		if arcToParent.direction > 0 {
			arcToParent.pushUpward(current, parent, arcToParent.capacity-arcToParent.flow)
		} else {
			arcToParent.pushDownward(current, parent, arcToParent.flow)
		}
	}

	if current.excess > 0 && prevEx <= 0 {
		if PseudoCtx.LowestLabel {
			lowestStrongLabel = current.label
		}
		current.addToStrongBucket(strongRoots[current.label])
	}
}

// static inline void
// breakRelationship (Node *oldParent, Node *child)
// (*node) breakRelationship
func (n *node) breakRelationship(child *node) {
	var current *node
	child.parent = nil

	if n.childList == child {
		n.childList = child.next
		child.next = nil
		return
	}

	for current = n.childList; current.next != child; current = current.next {
		current.next = child.next
		child.next = nil
	}
}

// static inline int
// addRelationship (Node *newParent, Node *child)
// (*node) addRelationship
// CLB: implement as static void function, calling code ignores return value
func (n *node) addRelationship(child *node) {
	child.parent = n
	child.next = n.childList
	n.childList = child
}

// static Arc *
// findWeakNode (Node *strongNode, Node **weakNode)
// (*node) findWeakNode() (*arc, weakNode *node)
// CLB: avoid pointer-to-pointer handling by also returning computed weakNode
func (n *node) findWeakNode() (*arc, *node) {
	var i, size uint
	var out *arc
	var weakNode *node

	size = n.numberOutOfTree

	for i = n.nextArc; i < size; i++ {
		stats.NumArcScans++
		if PseudoCtx.LowestLabel {
			if n.outOfTree[i].to.label == lowestStrongLabel-1 {
				n.nextArc = i
				out = n.outOfTree[i]
				weakNode = out.to
				n.numberOutOfTree--
				n.outOfTree[i] = n.outOfTree[n.numberOutOfTree]
				return out, weakNode
			}
			if n.outOfTree[i].from.label == (lowestStrongLabel - 1) {
				n.nextArc = i
				out = n.outOfTree[i]
				weakNode = out.from
				n.numberOutOfTree--
				n.outOfTree[i] = n.outOfTree[n.numberOutOfTree]
				return out, weakNode
			}
		} else {
			if n.outOfTree[i].to.label == (highestStrongLabel - 1) {
				n.nextArc = i
				out = n.outOfTree[i]
				weakNode = out.to
				n.numberOutOfTree--
				n.outOfTree[i] = n.outOfTree[n.numberOutOfTree]
				return out, weakNode
			}
			if n.outOfTree[i].from.label == (highestStrongLabel - 1) {
				n.nextArc = i
				out = n.outOfTree[i]
				weakNode = out.from
				n.numberOutOfTree--
				n.outOfTree[i] = n.outOfTree[n.numberOutOfTree]
				return out, weakNode
			}
		}
	}

	n.nextArc = n.numberOutOfTree
	return nil, nil

}

// (*node) checkChildren
func (n *node) checkChildren() {
	for ; n.nextScan != nil; n.nextScan = n.nextScan.next {
		if n.nextScan.label == n.label {
			return
		}
	}

	labelCount[n.label]--
	n.label++
	labelCount[n.label]++

	stats.NumRelabels++ // Always collect stats

	n.nextArc = 0
}

// static void
// liftAll (Node *rootNode)
// node.liftAll()
func (n *node) liftAll() {
	var temp *node
	current := n

	current.nextScan = current.childList

	labelCount[current.label]--
	current.label = numNodes

	for ; current != nil; current = current.parent {
		for current.nextScan != nil {
			temp = current.nextScan
			current.nextScan = current.nextScan.next
			current = temp
			current.nextScan = current.childList

			labelCount[current.label]--
			current.label = numNodes
		}
	}
}

func (n *node) addToStrongBucket(rootBucket *root) {
	if PseudoCtx.FifoBucket {
		if rootBucket.start != nil {
			rootBucket.end.next = n
			rootBucket.end = n
			n.next = nil
		} else {
			rootBucket.start = n
			rootBucket.end = n
			n.next = nil
		}
	} else {
		n.next = rootBucket.start
		rootBucket.start = n
		return
	}
}

// static void
// sort (Node * current)
func (n *node) sort() {
	if n.numberOutOfTree > uint(1) {
		quickSort(n.outOfTree, 0, n.numberOutOfTree-1)
	}
}

// static void
// minisort (Node *current)
func (n *node) minisort() {
	temp := n.outOfTree[n.nextArc]
	var i uint
	size := n.numberOutOfTree
	tempflow := temp.flow

	for i := n.nextArc + 1; i < size && tempflow < n.outOfTree[i].flow; i++ {
		n.outOfTree[i-1] = n.outOfTree[i]
	}
	n.outOfTree[i-1] = temp
}

// static void
// decompose (Node *excessNode, const uint source, uint *iteration)
// CLB: would prefer node.decompose(source) iteration, but keep mainline logic the same
//  node.decompose(source uint, interation *uint)
func (n *node) decompose(source uint, iteration *uint) {
	current := n
	var tempArc *arc
	bottleneck := n.excess

	for ; current.number != source && current.visited < *iteration; current = tempArc.from {
		current.visited = *iteration
		tempArc = current.outOfTree[current.nextArc]

		if tempArc.flow < bottleneck {
			bottleneck = tempArc.flow
		}
	}

	if current.number == source {
		n.excess -= bottleneck
		current = n

		for current.number != source {
			tempArc = current.outOfTree[current.nextArc]
			tempArc.flow -= bottleneck

			if tempArc.flow != 0 {
				current.minisort()
			} else {
				current.nextArc++
			}
			current = tempArc.from
		}
		return
	}

	*iteration++

	bottleneck = current.outOfTree[current.nextArc].flow
	for current.visited < *iteration {
		current.visited = *iteration
		tempArc = current.outOfTree[current.nextArc]

		if tempArc.flow < bottleneck {
			bottleneck = tempArc.flow
		}
		current = tempArc.from
	}

	*iteration++

	for current.visited < *iteration {
		current.visited = *iteration

		tempArc = current.outOfTree[current.nextArc]
		tempArc.flow -= bottleneck

		if tempArc.flow != 0 {
			current.minisort()
			current = tempArc.from
		} else {
			current.nextArc++
			current = tempArc.from
		}
	}
}

// =================== the root object
// allocations are in-line, as needed
type root struct {
	start *node
	end   *node
}

// ================ results

// static void
// checkOptimality (const uint gap)
// Internalize "gap" as in RecoverFlow.
func checkOptimality() []string {
	// setting gap value is taken out of main() in C source code
	var gap uint
	if PseudoCtx.LowestLabel {
		gap = lowestStrongLabel
	} else {
		gap = numNodes
	}

	var i uint
	var mincut uint
	var ret []string
	excess := make([]uint, numNodes)

	check := true
	for i = 0; i < numArcs; i++ {
		if arcList[i].from.label >= gap && arcList[i].to.label < gap {
			mincut += arcList[i].capacity
		}
		if arcList[i].flow > arcList[i].capacity || arcList[i].flow < 0 {
			check = false
			ret = append(ret,
				fmt.Sprintf("c Capacity constraint violated on arc (%d, %d). Flow = %d, capacity = %d",
					arcList[i].from.number,
					arcList[i].to.number,
					arcList[i].flow,
					arcList[i].capacity))
		}
		excess[arcList[i].from.number-1] -= arcList[i].flow
		excess[arcList[i].to.number-1] += arcList[i].flow
	}
	for i = 0; i < numNodes; i++ {
		if i != source-1 && i != sink-1 {
			if excess[i] != 0 {
				check = false
				ret = append(ret,
					fmt.Sprintf("c Flow balance constraint violated in node %d. Excess = %d",
						i+1,
						excess[i]))
			}
		}
	}
	if check {
		ret = append(ret, "c ", "c Solution checks as feasible")
	}
	fmt.Printf("Sink = %v", sink)
	fmt.Printf("Sink -1=%v", sink-1)
	check = true
	if excess[sink-1] != mincut {
		check = false
		ret = append(ret, "c ", "c Flow is not optimal - max flow does not equal min cut")
	}
	if check {
		ret = append(ret, "c ", "c Solution checks as optimal", "c Solution")
		ret = append(ret, fmt.Sprintf("s %d\n", mincut))
	}

	return ret
}

// static void
// displayFlow (void)
// C_source uses "a SRC DST FLOW" format; however, the examples we have,
// e.g., http://lpsolve.sourceforge.net/5.5/DIMACS_asn.htm, use
// "f SRC DST FLOW" format.  Here we use the latter, since we can
// then use the examples as test cases.
func displayFlow() []string {
	var ret []string
	for i := uint(0); i < numArcs; i++ {
		ret = append(ret,
			fmt.Sprintf("f %d %d %d", arcList[i].from.number, arcList[i].to.number, arcList[i].flow))
	}

	return ret
}

// ================ public functions =====================
type AlphaString string

func (a *AlphaString) Scan(state fmt.ScanState, verb rune) error {
	token, err := state.Token(true, unicode.IsLetter)
	if err != nil {
		return err
	}
	*a = AlphaString(token)
	return nil
}

// ReadDimacsFile implements readDimacsFile of C source code.
func ReadDimacsFile(fh *os.File) error {
	var i, numLines, from, to, first, last uint
	var capacity uint
	//var word []byte
	var ch, word AlphaString
	//var ch1 byte
	var ch1 AlphaString
	fmt.Println("Reading Dimacs\n")
	buf := bufio.NewReader(fh)
	var atEOF bool
	for {
		if atEOF {
			break
		}
		fmt.Println("Reading line")
		line, err := buf.ReadBytes('\n')
		fmt.Println("Reading line 2")
		if err != io.EOF {
			fmt.Println("err!=io.EOF")
			fmt.Print(err)
			//return err
		} else if err == io.EOF {
			fmt.Println("EOF")
			if len(line) == 0 {
				fmt.Println("EOF2")
				break // nothing more to process
			}
			// ... at EOF with data but no '\n' line termination.
			// While not necessary for os.STDIN; it can happen in a file.
			atEOF = true
		} else {
			// Strip off EOL.
			fmt.Println("Strip EOL")
			line = line[:len(line)-1]
		}
		fmt.Println("numLines++")
		numLines++
		fmt.Printf("line = %v", string(line))
		fmt.Printf("line[0] = %v", string(line[0]))

		switch line[0] {
		case 'p':
			fmt.Println("Case p ")
			if _, err := fmt.Sscanf(string(line), "%v %s %d %d", &ch, &word, &numNodes, &numArcs); err != nil {
				fmt.Println(err, ch, word, numNodes, numArcs)
				return err
			}

			adjacencyList = make([]*node, numNodes)
			strongRoots = make([]*root, numNodes)
			labelCount = make([]uint, numNodes)
			arcList = make([]*arc, numArcs)

			var i uint
			for i = 0; i < numNodes; i++ {
				strongRoots[i] = new(root)
				adjacencyList[i] = &node{number: i + 1}
				var u uint
				labelCount = append(labelCount, u)
			}
			for i = 0; i < numArcs; i++ {
				arcList[i] = &arc{direction: 1}
			}
			first = 0
			last = numArcs - 1
		case 'a':
			fmt.Println("Case a")
			if _, err := fmt.Sscanf(string(line), "%v %d %d %d", &ch, &from, &to, &capacity); err != nil {
				return err
			}
			if (from+to)%2 != 0 {
				arcList[first].from = adjacencyList[from-1]
				arcList[first].to = adjacencyList[to-1]
				arcList[first].capacity = capacity
				first++
			} else {
				arcList[last].from = adjacencyList[from-1]
				arcList[last].to = adjacencyList[to-1]
				arcList[last].capacity = capacity
				last--
			}

			adjacencyList[from-1].numAdjacent++
			adjacencyList[to-1].numAdjacent++
		case 'n':
			fmt.Println("Case n")
			if _, err := fmt.Sscanf(string(line), "%v %d %v", &ch, &i, &ch1); err != nil {
				fmt.Println(err, ch, i, ch1)
				return err
			}
			fmt.Println(ch, i, ch1)
			//ch1 = string(ch1)
			if ch1 == AlphaString('s') {
				fmt.Println("Found a source")
				source = i
			} else if ch1 == AlphaString('t') {
				fmt.Println("Found a sink")
				sink = i
			} else {
				return fmt.Errorf("unrecognized character %v on line %d", ch1, numLines)
			}
		case '\n', 'c':
			fmt.Println("Case newline or comment")
			continue // catches blank lines and "comment" lines - blank lines not in spec.
		default:
			fmt.Println("Case default")
			return fmt.Errorf("unknown data: %s", string(line))
		}
	}

	for i = 0; i < numNodes; i++ {
		adjacencyList[i].createOutOfTree()
	}

	for i = 0; i < numArcs; i++ {
		to = arcList[i].to.number
		from = arcList[i].from.number
		capacity = arcList[i].capacity

		if !(source == to || sink == from || from == to) {
			if source == from && to == sink {
				arcList[i].flow = capacity
			} else if from == source || to != sink {
				adjacencyList[from-1].addOutOfTreeNode(arcList[i])
			} else if to == sink {
				adjacencyList[to-1].addOutOfTreeNode(arcList[i])
			} else {
				adjacencyList[from-1].addOutOfTreeNode(arcList[i])
			}
		}
	}

	return nil
}

// SimpleInitialization implements simpleInitialization of C source code.
func SimpleInitialization() {
	var i, size uint
	var tempArc *arc

	// debug index out of range
	fmt.Printf("numArcs = %v\n", numArcs)
	fmt.Printf("numNodes = %v\n", numNodes)
	fmt.Printf("size = %v \n", size)
	fmt.Printf("source = %v \n", source)

	size = adjacencyList[source-1].numberOutOfTree
	for i = 0; i < size; i++ {
		tempArc = adjacencyList[source-1].outOfTree[i]
		tempArc.flow = tempArc.capacity
		tempArc.to.excess += tempArc.capacity
	}

	size = adjacencyList[sink-1].numberOutOfTree
	for i = 0; i < size; i++ {
		tempArc = adjacencyList[sink-1].outOfTree[i]
		tempArc.flow = tempArc.capacity
		tempArc.from.excess -= tempArc.capacity
	}

	adjacencyList[source-1].excess = 0
	adjacencyList[sink-1].excess = 0

	for i = 0; i < numNodes; i++ {
		if adjacencyList[i].excess > 0 {
			adjacencyList[i].label = 1
			labelCount[1]++
			adjacencyList[i].addToStrongBucket(strongRoots[1])
		}
	}

	adjacencyList[source-1].label = numNodes
	adjacencyList[sink-1].label = 0
	labelCount[0] = (numNodes - 2) - labelCount[1]
}

// FlowPhaseOne implements pseudoFlowPhaseOne of C source code.
func FlowPhaseOne() {
	var strongRoot *node

	if PseudoCtx.LowestLabel {
		strongRoot = getLowestStrongRoot()
		for ; strongRoot != nil; strongRoot = getLowestStrongRoot() {
			strongRoot.processRoot()
		}
	} else {
		strongRoot = getHighestStrongRoot()
		for ; strongRoot != nil; strongRoot = getHighestStrongRoot() {
			strongRoot.processRoot()
		}
	}
}

// static void
// recoverFlow (const uint gap)

// RecoverFlow implements recoverFlow of C source code.
// It internalizes setting 'gap' value.
func RecoverFlow() {
	// setting gap value is taken out of main() in C source code
	var gap uint
	if PseudoCtx.LowestLabel {
		gap = lowestStrongLabel
	} else {
		gap = numNodes
	}

	var i, j uint
	iteration := uint(1)
	var tempArc *arc
	var tempNode *node

	for i = 0; i < adjacencyList[sink-1].numberOutOfTree; i++ {
		tempArc = adjacencyList[sink-1].outOfTree[i]
		if tempArc.from.excess < uint(0) {
			if tempArc.from.excess+tempArc.flow < uint(0) {
				tempArc.from.excess += tempArc.flow
				tempArc.flow = uint(0)
			} else {
				tempArc.flow = tempArc.from.excess + tempArc.flow
				tempArc.from.excess = uint(0)
			}
		}
	}

	for i = 0; i < adjacencyList[source-1].numberOutOfTree; i++ {
		tempArc = adjacencyList[source-1].outOfTree[i]
		tempArc.to.addOutOfTreeNode(tempArc)
	}

	adjacencyList[source-1].excess = uint(0)
	adjacencyList[sink-1].excess = uint(0)

	for i = 0; i < numNodes; i++ {
		tempNode = adjacencyList[i]
		if i == source-1 || i == sink-1 {
			continue
		}

		if tempNode.label >= gap {
			tempNode.nextArc = 0
			if tempNode.parent != nil && tempNode.arcToParent.flow != 0 {
				tempNode.arcToParent.to.addOutOfTreeNode(tempNode.arcToParent)
			}

			for j = 0; j < tempNode.numberOutOfTree; j++ {
				if tempNode.outOfTree[j].flow != 0 {
					tempNode.numberOutOfTree--
					tempNode.outOfTree[j] = tempNode.outOfTree[tempNode.numberOutOfTree]
					j--
				}
			}

			tempNode.sort()
		}
	}

	for i = 0; i < numNodes; i++ {
		tempNode = adjacencyList[i]
		for tempNode.excess > 0 {
			iteration++
			tempNode.decompose(source, &iteration)
		}
	}
}

// Result returns scan of arc/node results in Dimac syntax.
//
// Example for input file "maxflow.net":
//	c <header>
//	c
//	c Dimacs-format maximum flow result file
//	c generated by pseudo.go
//	c
//	c Optimal flow using Hochbaum's PseudoFlow algorithm"
//	c
//	c Runtime Configuration:
//	c Lowest label pseudoflow algorithm
//	c Using LIFO buckets
//	c
//	c Solution checks as feasible.
//	c
//	c Solution checks as optimal
//	c Solution
//	s 15
//	c
//	c SRC DST FLOW
//	f 1 2 5
//	f 1 3 10
//	...
func Result(header string) []string {
	// header and runtime config info
	ret := []string{
		"c " + header,
		"c ",
		"c Dimacs-format maximum flow result file",
		"c generated by pseudo.go",
		"c ",
		"c Optimal flow using  Hochbaum's PseudoFlow algorithm",
		"c ",
		"c Runtime Configuration -"}

	if PseudoCtx.LowestLabel {
		ret = append(ret, "c Lowest label pseudoflow algorithm")
	} else {
		ret = append(ret, "c Highest label pseudoflow algorithm")
	}
	if PseudoCtx.FifoBucket {
		ret = append(ret, "c Using FIFO buckets")
	} else {
		ret = append(ret, "c Using LIFO buckets")
	}
	ret = append(ret, "c ")

	// add Solution
	ret = append(ret, checkOptimality()...)

	// add flows
	ret = append(ret, "c ", "c SRC DST FLOW")
	ret = append(ret, displayFlow()...)

	return ret
}

// timing info in case someone wants it as in C source main()
var timer = struct {
	start, readfile, initialize, flow, recflow time.Time
}{}

// TimerJSON return timings of the 4 processing steps of Run -
// ReadDimacsFile, SimpleInitialization, FlowPhaseOne, and RecoverFlow.
// Note: the file initialization and result marshaling times are not
// included in result.
func TimerJSON() string {
	type times struct {
		ReadDimacsFile, SimpleInitialization, FlowPhaseOne, RecoverFlow, Total time.Duration
	}
	data := times{
		timer.readfile.Sub(timer.start),
		timer.initialize.Sub(timer.readfile),
		timer.flow.Sub(timer.initialize),
		timer.recflow.Sub(timer.flow),
		timer.recflow.Sub(timer.start),
	}
	j, _ := json.Marshal(data)
	return string(j)
}

// Run takes an input file and returns Result having
// called all public functions in sequence. If input == "stdin"
// then os.Stdin is read.
func Run(input string) ([]string, error) {
	var fh *os.File
	var err error
	if strings.ToLower(input) == "stdin" {
		fh = os.Stdin
	} else {
		fh, err = os.Open(input)
		if err != nil {
			return nil, err
		}
	}
	defer fh.Close()

	// implement C source main()
	timer.start = time.Now()
	if err = ReadDimacsFile(fh); err != nil {
		return nil, err
	}
	timer.readfile = time.Now()
	SimpleInitialization()
	timer.initialize = time.Now()
	FlowPhaseOne()
	timer.flow = time.Now()
	RecoverFlow()
	timer.recflow = time.Now()
	ret := Result("Data: " + input)

	return ret, nil
}

// ======================== quicksort implementation

// static void
// quickSort (Arc **arr, const uint first, const uint last)
// CLB: **Arc value is []*arc; slices manipulate the backing array
func quickSort(arr []*arc, first, last uint) {
	left, right := first, last
	var swap *arc

	// Bubble sort if 5 elements or less
	if (right - left) <= 5 {
		for i := right; i > left; i++ {
			swap = nil
			for j := left; j < i; j++ {
				if arr[j].flow < arr[j+1].flow {
					swap = arr[j]
					arr[j] = arr[j+1]
					arr[j+1] = swap
				}
			}
			if swap != nil {
				return
			}
		}
		return
	}

	pivot := (first + last) / 2
	x1 := arr[first].flow
	x2 := arr[pivot].flow // was: arr[mid]
	x3 := arr[last].flow

	if x1 <= x2 {
		if x2 > x3 {
			pivot = left
			if x1 <= x3 {
				pivot = right
			}
		}
	} else {
		if x2 <= x3 {
			pivot = right
			if x1 <= x3 {
				pivot = left
			}
		}
	}

	pivotval := arr[pivot].flow
	swap = arr[first]
	arr[first] = arr[pivot]
	arr[pivot] = swap

	left = first + 1

	for left < right {
		if arr[left].flow < pivotval {
			swap = arr[left]
			arr[left] = arr[right]
			arr[right] = swap
			right--
		} else {
			left++
		}
	}

	swap = arr[first]
	arr[first] = arr[left]
	arr[left] = swap

	if first < (left - 1) {
		quickSort(arr, first, left-1)
	}
	if left+1 < last {
		quickSort(arr, left+1, last)
	}
}
