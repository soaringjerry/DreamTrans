package pcas

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

type fakeServerStream struct {
	ctx      context.Context
	messages []*anypb.Any
}

func (s *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)       {}
func (s *fakeServerStream) Context() context.Context     { return s.ctx }
func (s *fakeServerStream) SendMsg(interface{}) error    { return nil }

func (s *fakeServerStream) RecvMsg(message interface{}) error {
	if len(s.messages) == 0 {
		return io.EOF
	}
	target := message.(*anypb.Any)
	target.TypeUrl = s.messages[0].TypeUrl
	target.Value = append(target.Value[:0], s.messages[0].Value...)
	s.messages = s.messages[1:]
	return nil
}

func TestTranscribeStreamRequiresConfigFirst(t *testing.T) {
	provider := &Provider{}
	stream := &fakeServerStream{
		ctx: context.Background(),
		messages: []*anypb.Any{{
			TypeUrl: "audio",
			Value:   []byte("not configuration"),
		}},
	}

	err := provider.TranscribeStream(stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code = %v, want %v (err: %v)", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestTranscribeStreamRejectsInvalidLanguage(t *testing.T) {
	provider := &Provider{}
	stream := &fakeServerStream{
		ctx: context.Background(),
		messages: []*anypb.Any{{
			TypeUrl: "config",
			Value:   []byte("language=../../secret,enable_partials=false"),
		}},
	}

	err := provider.TranscribeStream(stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code = %v, want %v (err: %v)", status.Code(err), codes.InvalidArgument, err)
	}
}
