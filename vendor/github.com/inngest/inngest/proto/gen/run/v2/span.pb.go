// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.1
// 	protoc        (unknown)
// source: run/v2/span.proto

package runv2

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Span struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id         *SpanIdentifier        `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Ctx        *SpanContext           `protobuf:"bytes,2,opt,name=ctx,proto3" json:"ctx,omitempty"`
	Name       string                 `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	Kind       SpanStepOp             `protobuf:"varint,4,opt,name=kind,proto3,enum=run.v2.SpanStepOp" json:"kind,omitempty"`
	Status     SpanStatus             `protobuf:"varint,5,opt,name=status,proto3,enum=run.v2.SpanStatus" json:"status,omitempty"`
	StatusCode string                 `protobuf:"bytes,6,opt,name=status_code,json=statusCode,proto3" json:"status_code,omitempty"`
	Scope      string                 `protobuf:"bytes,7,opt,name=scope,proto3" json:"scope,omitempty"`
	Timestamp  *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	DurationMs int64                  `protobuf:"varint,9,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	Attributes map[string]string      `protobuf:"bytes,10,rep,name=attributes,proto3" json:"attributes,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	Triggers   []*Trigger             `protobuf:"bytes,11,rep,name=triggers,proto3" json:"triggers,omitempty"`
	Output     []byte                 `protobuf:"bytes,12,opt,name=output,proto3" json:"output,omitempty"`
	Links      []*SpanLink            `protobuf:"bytes,13,rep,name=links,proto3" json:"links,omitempty"`
	Events     []*SpanEvent           `protobuf:"bytes,14,rep,name=events,proto3" json:"events,omitempty"`
	Input      []byte                 `protobuf:"bytes,15,opt,name=input,proto3" json:"input,omitempty"`
}

func (x *Span) Reset() {
	*x = Span{}
	mi := &file_run_v2_span_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Span) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Span) ProtoMessage() {}

func (x *Span) ProtoReflect() protoreflect.Message {
	mi := &file_run_v2_span_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Span.ProtoReflect.Descriptor instead.
func (*Span) Descriptor() ([]byte, []int) {
	return file_run_v2_span_proto_rawDescGZIP(), []int{0}
}

func (x *Span) GetId() *SpanIdentifier {
	if x != nil {
		return x.Id
	}
	return nil
}

func (x *Span) GetCtx() *SpanContext {
	if x != nil {
		return x.Ctx
	}
	return nil
}

func (x *Span) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Span) GetKind() SpanStepOp {
	if x != nil {
		return x.Kind
	}
	return SpanStepOp_RUN
}

func (x *Span) GetStatus() SpanStatus {
	if x != nil {
		return x.Status
	}
	return SpanStatus_UNKNOWN
}

func (x *Span) GetStatusCode() string {
	if x != nil {
		return x.StatusCode
	}
	return ""
}

func (x *Span) GetScope() string {
	if x != nil {
		return x.Scope
	}
	return ""
}

func (x *Span) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

func (x *Span) GetDurationMs() int64 {
	if x != nil {
		return x.DurationMs
	}
	return 0
}

func (x *Span) GetAttributes() map[string]string {
	if x != nil {
		return x.Attributes
	}
	return nil
}

func (x *Span) GetTriggers() []*Trigger {
	if x != nil {
		return x.Triggers
	}
	return nil
}

func (x *Span) GetOutput() []byte {
	if x != nil {
		return x.Output
	}
	return nil
}

func (x *Span) GetLinks() []*SpanLink {
	if x != nil {
		return x.Links
	}
	return nil
}

func (x *Span) GetEvents() []*SpanEvent {
	if x != nil {
		return x.Events
	}
	return nil
}

func (x *Span) GetInput() []byte {
	if x != nil {
		return x.Input
	}
	return nil
}

type SpanIdentifier struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	AccountId  string `protobuf:"bytes,1,opt,name=account_id,json=accountId,proto3" json:"account_id,omitempty"`
	EnvId      string `protobuf:"bytes,2,opt,name=env_id,json=envId,proto3" json:"env_id,omitempty"`
	AppId      string `protobuf:"bytes,3,opt,name=app_id,json=appId,proto3" json:"app_id,omitempty"`
	FunctionId string `protobuf:"bytes,4,opt,name=function_id,json=functionId,proto3" json:"function_id,omitempty"`
	RunId      string `protobuf:"bytes,5,opt,name=run_id,json=runId,proto3" json:"run_id,omitempty"`
}

func (x *SpanIdentifier) Reset() {
	*x = SpanIdentifier{}
	mi := &file_run_v2_span_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SpanIdentifier) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SpanIdentifier) ProtoMessage() {}

func (x *SpanIdentifier) ProtoReflect() protoreflect.Message {
	mi := &file_run_v2_span_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SpanIdentifier.ProtoReflect.Descriptor instead.
func (*SpanIdentifier) Descriptor() ([]byte, []int) {
	return file_run_v2_span_proto_rawDescGZIP(), []int{1}
}

func (x *SpanIdentifier) GetAccountId() string {
	if x != nil {
		return x.AccountId
	}
	return ""
}

func (x *SpanIdentifier) GetEnvId() string {
	if x != nil {
		return x.EnvId
	}
	return ""
}

func (x *SpanIdentifier) GetAppId() string {
	if x != nil {
		return x.AppId
	}
	return ""
}

func (x *SpanIdentifier) GetFunctionId() string {
	if x != nil {
		return x.FunctionId
	}
	return ""
}

func (x *SpanIdentifier) GetRunId() string {
	if x != nil {
		return x.RunId
	}
	return ""
}

type SpanContext struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	TraceId      string  `protobuf:"bytes,1,opt,name=trace_id,json=traceId,proto3" json:"trace_id,omitempty"`
	ParentSpanId *string `protobuf:"bytes,2,opt,name=parent_span_id,json=parentSpanId,proto3,oneof" json:"parent_span_id,omitempty"`
	SpanId       string  `protobuf:"bytes,3,opt,name=span_id,json=spanId,proto3" json:"span_id,omitempty"`
}

func (x *SpanContext) Reset() {
	*x = SpanContext{}
	mi := &file_run_v2_span_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SpanContext) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SpanContext) ProtoMessage() {}

func (x *SpanContext) ProtoReflect() protoreflect.Message {
	mi := &file_run_v2_span_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SpanContext.ProtoReflect.Descriptor instead.
func (*SpanContext) Descriptor() ([]byte, []int) {
	return file_run_v2_span_proto_rawDescGZIP(), []int{2}
}

func (x *SpanContext) GetTraceId() string {
	if x != nil {
		return x.TraceId
	}
	return ""
}

func (x *SpanContext) GetParentSpanId() string {
	if x != nil && x.ParentSpanId != nil {
		return *x.ParentSpanId
	}
	return ""
}

func (x *SpanContext) GetSpanId() string {
	if x != nil {
		return x.SpanId
	}
	return ""
}

type Trigger struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	InternalId string `protobuf:"bytes,1,opt,name=internal_id,json=internalId,proto3" json:"internal_id,omitempty"` // Event ULID
	Body       []byte `protobuf:"bytes,2,opt,name=body,proto3" json:"body,omitempty"`
}

func (x *Trigger) Reset() {
	*x = Trigger{}
	mi := &file_run_v2_span_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Trigger) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Trigger) ProtoMessage() {}

func (x *Trigger) ProtoReflect() protoreflect.Message {
	mi := &file_run_v2_span_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Trigger.ProtoReflect.Descriptor instead.
func (*Trigger) Descriptor() ([]byte, []int) {
	return file_run_v2_span_proto_rawDescGZIP(), []int{3}
}

func (x *Trigger) GetInternalId() string {
	if x != nil {
		return x.InternalId
	}
	return ""
}

func (x *Trigger) GetBody() []byte {
	if x != nil {
		return x.Body
	}
	return nil
}

type SpanEvent struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Timestamp  *timestamppb.Timestamp `protobuf:"bytes,1,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	Name       string                 `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Attributes map[string]string      `protobuf:"bytes,3,rep,name=attributes,proto3" json:"attributes,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (x *SpanEvent) Reset() {
	*x = SpanEvent{}
	mi := &file_run_v2_span_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SpanEvent) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SpanEvent) ProtoMessage() {}

func (x *SpanEvent) ProtoReflect() protoreflect.Message {
	mi := &file_run_v2_span_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SpanEvent.ProtoReflect.Descriptor instead.
func (*SpanEvent) Descriptor() ([]byte, []int) {
	return file_run_v2_span_proto_rawDescGZIP(), []int{4}
}

func (x *SpanEvent) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

func (x *SpanEvent) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *SpanEvent) GetAttributes() map[string]string {
	if x != nil {
		return x.Attributes
	}
	return nil
}

type SpanLink struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	TraceId    string            `protobuf:"bytes,1,opt,name=trace_id,json=traceId,proto3" json:"trace_id,omitempty"`
	SpanId     string            `protobuf:"bytes,2,opt,name=span_id,json=spanId,proto3" json:"span_id,omitempty"`
	TraceState string            `protobuf:"bytes,3,opt,name=trace_state,json=traceState,proto3" json:"trace_state,omitempty"`
	Attributes map[string]string `protobuf:"bytes,4,rep,name=attributes,proto3" json:"attributes,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (x *SpanLink) Reset() {
	*x = SpanLink{}
	mi := &file_run_v2_span_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SpanLink) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SpanLink) ProtoMessage() {}

func (x *SpanLink) ProtoReflect() protoreflect.Message {
	mi := &file_run_v2_span_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SpanLink.ProtoReflect.Descriptor instead.
func (*SpanLink) Descriptor() ([]byte, []int) {
	return file_run_v2_span_proto_rawDescGZIP(), []int{5}
}

func (x *SpanLink) GetTraceId() string {
	if x != nil {
		return x.TraceId
	}
	return ""
}

func (x *SpanLink) GetSpanId() string {
	if x != nil {
		return x.SpanId
	}
	return ""
}

func (x *SpanLink) GetTraceState() string {
	if x != nil {
		return x.TraceState
	}
	return ""
}

func (x *SpanLink) GetAttributes() map[string]string {
	if x != nil {
		return x.Attributes
	}
	return nil
}

var File_run_v2_span_proto protoreflect.FileDescriptor

var file_run_v2_span_proto_rawDesc = []byte{
	0x0a, 0x11, 0x72, 0x75, 0x6e, 0x2f, 0x76, 0x32, 0x2f, 0x73, 0x70, 0x61, 0x6e, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x04, 0x73, 0x70, 0x61, 0x6e, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x10, 0x72, 0x75, 0x6e, 0x2f,
	0x76, 0x32, 0x2f, 0x72, 0x75, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xee, 0x04, 0x0a,
	0x04, 0x53, 0x70, 0x61, 0x6e, 0x12, 0x24, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x14, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e, 0x53, 0x70, 0x61, 0x6e, 0x49, 0x64, 0x65,
	0x6e, 0x74, 0x69, 0x66, 0x69, 0x65, 0x72, 0x52, 0x02, 0x69, 0x64, 0x12, 0x23, 0x0a, 0x03, 0x63,
	0x74, 0x78, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e,
	0x53, 0x70, 0x61, 0x6e, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74, 0x52, 0x03, 0x63, 0x74, 0x78,
	0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04,
	0x6e, 0x61, 0x6d, 0x65, 0x12, 0x26, 0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x0e, 0x32, 0x12, 0x2e, 0x72, 0x75, 0x6e, 0x2e, 0x76, 0x32, 0x2e, 0x53, 0x70, 0x61, 0x6e,
	0x53, 0x74, 0x65, 0x70, 0x4f, 0x70, 0x52, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x12, 0x2a, 0x0a, 0x06,
	0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x12, 0x2e, 0x72,
	0x75, 0x6e, 0x2e, 0x76, 0x32, 0x2e, 0x53, 0x70, 0x61, 0x6e, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73,
	0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x1f, 0x0a, 0x0b, 0x73, 0x74, 0x61, 0x74,
	0x75, 0x73, 0x5f, 0x63, 0x6f, 0x64, 0x65, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x73,
	0x74, 0x61, 0x74, 0x75, 0x73, 0x43, 0x6f, 0x64, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x73, 0x63, 0x6f,
	0x70, 0x65, 0x18, 0x07, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x73, 0x63, 0x6f, 0x70, 0x65, 0x12,
	0x38, 0x0a, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x08, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x09,
	0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x12, 0x1f, 0x0a, 0x0b, 0x64, 0x75, 0x72,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x6d, 0x73, 0x18, 0x09, 0x20, 0x01, 0x28, 0x03, 0x52, 0x0a,
	0x64, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x4d, 0x73, 0x12, 0x3a, 0x0a, 0x0a, 0x61, 0x74,
	0x74, 0x72, 0x69, 0x62, 0x75, 0x74, 0x65, 0x73, 0x18, 0x0a, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1a,
	0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e, 0x53, 0x70, 0x61, 0x6e, 0x2e, 0x41, 0x74, 0x74, 0x72, 0x69,
	0x62, 0x75, 0x74, 0x65, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x0a, 0x61, 0x74, 0x74, 0x72,
	0x69, 0x62, 0x75, 0x74, 0x65, 0x73, 0x12, 0x29, 0x0a, 0x08, 0x74, 0x72, 0x69, 0x67, 0x67, 0x65,
	0x72, 0x73, 0x18, 0x0b, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0d, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e,
	0x54, 0x72, 0x69, 0x67, 0x67, 0x65, 0x72, 0x52, 0x08, 0x74, 0x72, 0x69, 0x67, 0x67, 0x65, 0x72,
	0x73, 0x12, 0x16, 0x0a, 0x06, 0x6f, 0x75, 0x74, 0x70, 0x75, 0x74, 0x18, 0x0c, 0x20, 0x01, 0x28,
	0x0c, 0x52, 0x06, 0x6f, 0x75, 0x74, 0x70, 0x75, 0x74, 0x12, 0x24, 0x0a, 0x05, 0x6c, 0x69, 0x6e,
	0x6b, 0x73, 0x18, 0x0d, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0e, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e,
	0x53, 0x70, 0x61, 0x6e, 0x4c, 0x69, 0x6e, 0x6b, 0x52, 0x05, 0x6c, 0x69, 0x6e, 0x6b, 0x73, 0x12,
	0x27, 0x0a, 0x06, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x73, 0x18, 0x0e, 0x20, 0x03, 0x28, 0x0b, 0x32,
	0x0f, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e, 0x53, 0x70, 0x61, 0x6e, 0x45, 0x76, 0x65, 0x6e, 0x74,
	0x52, 0x06, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x73, 0x12, 0x14, 0x0a, 0x05, 0x69, 0x6e, 0x70, 0x75,
	0x74, 0x18, 0x0f, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x05, 0x69, 0x6e, 0x70, 0x75, 0x74, 0x1a, 0x3d,
	0x0a, 0x0f, 0x41, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74, 0x65, 0x73, 0x45, 0x6e, 0x74, 0x72,
	0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03,
	0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x95, 0x01,
	0x0a, 0x0e, 0x53, 0x70, 0x61, 0x6e, 0x49, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x66, 0x69, 0x65, 0x72,
	0x12, 0x1d, 0x0a, 0x0a, 0x61, 0x63, 0x63, 0x6f, 0x75, 0x6e, 0x74, 0x5f, 0x69, 0x64, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x61, 0x63, 0x63, 0x6f, 0x75, 0x6e, 0x74, 0x49, 0x64, 0x12,
	0x15, 0x0a, 0x06, 0x65, 0x6e, 0x76, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x05, 0x65, 0x6e, 0x76, 0x49, 0x64, 0x12, 0x15, 0x0a, 0x06, 0x61, 0x70, 0x70, 0x5f, 0x69, 0x64,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x61, 0x70, 0x70, 0x49, 0x64, 0x12, 0x1f, 0x0a,
	0x0b, 0x66, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0a, 0x66, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x64, 0x12, 0x15,
	0x0a, 0x06, 0x72, 0x75, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05,
	0x72, 0x75, 0x6e, 0x49, 0x64, 0x22, 0x7f, 0x0a, 0x0b, 0x53, 0x70, 0x61, 0x6e, 0x43, 0x6f, 0x6e,
	0x74, 0x65, 0x78, 0x74, 0x12, 0x19, 0x0a, 0x08, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x74, 0x72, 0x61, 0x63, 0x65, 0x49, 0x64, 0x12,
	0x29, 0x0a, 0x0e, 0x70, 0x61, 0x72, 0x65, 0x6e, 0x74, 0x5f, 0x73, 0x70, 0x61, 0x6e, 0x5f, 0x69,
	0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x48, 0x00, 0x52, 0x0c, 0x70, 0x61, 0x72, 0x65, 0x6e,
	0x74, 0x53, 0x70, 0x61, 0x6e, 0x49, 0x64, 0x88, 0x01, 0x01, 0x12, 0x17, 0x0a, 0x07, 0x73, 0x70,
	0x61, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x70, 0x61,
	0x6e, 0x49, 0x64, 0x42, 0x11, 0x0a, 0x0f, 0x5f, 0x70, 0x61, 0x72, 0x65, 0x6e, 0x74, 0x5f, 0x73,
	0x70, 0x61, 0x6e, 0x5f, 0x69, 0x64, 0x22, 0x3e, 0x0a, 0x07, 0x54, 0x72, 0x69, 0x67, 0x67, 0x65,
	0x72, 0x12, 0x1f, 0x0a, 0x0b, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x5f, 0x69, 0x64,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c,
	0x49, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x62, 0x6f, 0x64, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c,
	0x52, 0x04, 0x62, 0x6f, 0x64, 0x79, 0x22, 0xd9, 0x01, 0x0a, 0x09, 0x53, 0x70, 0x61, 0x6e, 0x45,
	0x76, 0x65, 0x6e, 0x74, 0x12, 0x38, 0x0a, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d,
	0x70, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74,
	0x61, 0x6d, 0x70, 0x52, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x12, 0x12,
	0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x12, 0x3f, 0x0a, 0x0a, 0x61, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74, 0x65, 0x73,
	0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1f, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e, 0x53, 0x70,
	0x61, 0x6e, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x2e, 0x41, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74,
	0x65, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x0a, 0x61, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75,
	0x74, 0x65, 0x73, 0x1a, 0x3d, 0x0a, 0x0f, 0x41, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74, 0x65,
	0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75,
	0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02,
	0x38, 0x01, 0x22, 0xde, 0x01, 0x0a, 0x08, 0x53, 0x70, 0x61, 0x6e, 0x4c, 0x69, 0x6e, 0x6b, 0x12,
	0x19, 0x0a, 0x08, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x74, 0x72, 0x61, 0x63, 0x65, 0x49, 0x64, 0x12, 0x17, 0x0a, 0x07, 0x73, 0x70,
	0x61, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x70, 0x61,
	0x6e, 0x49, 0x64, 0x12, 0x1f, 0x0a, 0x0b, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x73, 0x74, 0x61,
	0x74, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x74, 0x72, 0x61, 0x63, 0x65, 0x53,
	0x74, 0x61, 0x74, 0x65, 0x12, 0x3e, 0x0a, 0x0a, 0x61, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74,
	0x65, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1e, 0x2e, 0x73, 0x70, 0x61, 0x6e, 0x2e,
	0x53, 0x70, 0x61, 0x6e, 0x4c, 0x69, 0x6e, 0x6b, 0x2e, 0x41, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75,
	0x74, 0x65, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x0a, 0x61, 0x74, 0x74, 0x72, 0x69, 0x62,
	0x75, 0x74, 0x65, 0x73, 0x1a, 0x3d, 0x0a, 0x0f, 0x41, 0x74, 0x74, 0x72, 0x69, 0x62, 0x75, 0x74,
	0x65, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c,
	0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a,
	0x02, 0x38, 0x01, 0x42, 0x33, 0x5a, 0x31, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f,
	0x6d, 0x2f, 0x69, 0x6e, 0x6e, 0x67, 0x65, 0x73, 0x74, 0x2f, 0x69, 0x6e, 0x6e, 0x67, 0x65, 0x73,
	0x74, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x72, 0x75, 0x6e, 0x2f,
	0x76, 0x32, 0x3b, 0x72, 0x75, 0x6e, 0x76, 0x32, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_run_v2_span_proto_rawDescOnce sync.Once
	file_run_v2_span_proto_rawDescData = file_run_v2_span_proto_rawDesc
)

func file_run_v2_span_proto_rawDescGZIP() []byte {
	file_run_v2_span_proto_rawDescOnce.Do(func() {
		file_run_v2_span_proto_rawDescData = protoimpl.X.CompressGZIP(file_run_v2_span_proto_rawDescData)
	})
	return file_run_v2_span_proto_rawDescData
}

var file_run_v2_span_proto_msgTypes = make([]protoimpl.MessageInfo, 9)
var file_run_v2_span_proto_goTypes = []any{
	(*Span)(nil),                  // 0: span.Span
	(*SpanIdentifier)(nil),        // 1: span.SpanIdentifier
	(*SpanContext)(nil),           // 2: span.SpanContext
	(*Trigger)(nil),               // 3: span.Trigger
	(*SpanEvent)(nil),             // 4: span.SpanEvent
	(*SpanLink)(nil),              // 5: span.SpanLink
	nil,                           // 6: span.Span.AttributesEntry
	nil,                           // 7: span.SpanEvent.AttributesEntry
	nil,                           // 8: span.SpanLink.AttributesEntry
	(SpanStepOp)(0),               // 9: run.v2.SpanStepOp
	(SpanStatus)(0),               // 10: run.v2.SpanStatus
	(*timestamppb.Timestamp)(nil), // 11: google.protobuf.Timestamp
}
var file_run_v2_span_proto_depIdxs = []int32{
	1,  // 0: span.Span.id:type_name -> span.SpanIdentifier
	2,  // 1: span.Span.ctx:type_name -> span.SpanContext
	9,  // 2: span.Span.kind:type_name -> run.v2.SpanStepOp
	10, // 3: span.Span.status:type_name -> run.v2.SpanStatus
	11, // 4: span.Span.timestamp:type_name -> google.protobuf.Timestamp
	6,  // 5: span.Span.attributes:type_name -> span.Span.AttributesEntry
	3,  // 6: span.Span.triggers:type_name -> span.Trigger
	5,  // 7: span.Span.links:type_name -> span.SpanLink
	4,  // 8: span.Span.events:type_name -> span.SpanEvent
	11, // 9: span.SpanEvent.timestamp:type_name -> google.protobuf.Timestamp
	7,  // 10: span.SpanEvent.attributes:type_name -> span.SpanEvent.AttributesEntry
	8,  // 11: span.SpanLink.attributes:type_name -> span.SpanLink.AttributesEntry
	12, // [12:12] is the sub-list for method output_type
	12, // [12:12] is the sub-list for method input_type
	12, // [12:12] is the sub-list for extension type_name
	12, // [12:12] is the sub-list for extension extendee
	0,  // [0:12] is the sub-list for field type_name
}

func init() { file_run_v2_span_proto_init() }
func file_run_v2_span_proto_init() {
	if File_run_v2_span_proto != nil {
		return
	}
	file_run_v2_run_proto_init()
	file_run_v2_span_proto_msgTypes[2].OneofWrappers = []any{}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_run_v2_span_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   9,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_run_v2_span_proto_goTypes,
		DependencyIndexes: file_run_v2_span_proto_depIdxs,
		MessageInfos:      file_run_v2_span_proto_msgTypes,
	}.Build()
	File_run_v2_span_proto = out.File
	file_run_v2_span_proto_rawDesc = nil
	file_run_v2_span_proto_goTypes = nil
	file_run_v2_span_proto_depIdxs = nil
}
