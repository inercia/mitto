package conversation

import "context"

func (*queueTitleRetryProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

func (*queueTitleSuccessProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

func (*reapedCloseProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

func (*captureAuxProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

func (*quiescenceTitleProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

func (*wedgeProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}
