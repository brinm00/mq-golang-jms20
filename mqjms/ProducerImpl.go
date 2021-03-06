// Copyright (c) IBM Corporation 2019.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0, which is available at
// http://www.eclipse.org/legal/epl-2.0.
//
// SPDX-License-Identifier: EPL-2.0

// Package mqjms provides the implementation of the JMS style Golang interfaces to communicate with IBM MQ.
package mqjms

import (
	"fmt"
	"log"
	"strconv"

	"github.com/ibm-messaging/mq-golang-jms20/jms20subset"
	ibmmq "github.com/ibm-messaging/mq-golang/v5/ibmmq"
)

// ProducerImpl defines a struct that contains the necessary objects for
// sending messages to a queue on an IBM MQ queue manager.
type ProducerImpl struct {
	ctx          ContextImpl
	deliveryMode int
	timeToLive   int
}

// SendString sends a TextMessage with the specified body to the specified Destination
// using any message options that are defined on this JMSProducer.
func (producer ProducerImpl) SendString(dest jms20subset.Destination, bodyStr string) jms20subset.JMSException {

	// This is essentially just a helper method that avoids the application having
	// to create its own TextMessage object.
	msg := producer.ctx.CreateTextMessage()
	msg.SetText(bodyStr)

	return producer.Send(dest, msg)

}

// SendBytes sends a BytesMessage with the specified body to the specified Destination
// using any message options that are defined on this JMSProducer.
func (producer ProducerImpl) SendBytes(dest jms20subset.Destination, body []byte) jms20subset.JMSException {

	// This is essentially just a helper method that avoids the application having
	// to create its own TextMessage object.
	msg := producer.ctx.CreateBytesMessage()
	msg.WriteBytes(body)

	return producer.Send(dest, msg)

}

// Send a message to the specified IBM MQ queue, using the message options
// that are defined on this JMSProducer.
func (producer ProducerImpl) Send(dest jms20subset.Destination, msg jms20subset.Message) jms20subset.JMSException {

	// Set up the basic objects we need to send the message.
	mqod := ibmmq.NewMQOD()

	var openOptions int32
	openOptions = ibmmq.MQOO_OUTPUT + ibmmq.MQOO_FAIL_IF_QUIESCING
	openOptions |= ibmmq.MQOO_INPUT_AS_Q_DEF

	mqod.ObjectType = ibmmq.MQOT_Q
	mqod.ObjectName = dest.GetDestinationName()

	var retErr jms20subset.JMSException

	// Invoke the MQ command to open the queue, and register a defer hook
	// to automatically close the object once we exit this function.
	qObject, err := producer.ctx.qMgr.Open(mqod, openOptions)
	if (ibmmq.MQObject{}) != qObject {
		defer qObject.Close(0)
	}

	if err == nil {

		// Successfully opened the queue, so now prepare to send the message.
		putmqmd := ibmmq.NewMQMD()
		pmo := ibmmq.NewMQPMO()

		// Calculate the syncpoint value
		syncpointSetting := ibmmq.MQPMO_NO_SYNCPOINT
		if producer.ctx.sessionMode == jms20subset.JMSContextSESSIONTRANSACTED {
			syncpointSetting = ibmmq.MQPMO_SYNCPOINT
		}

		// Configure the put message options, including asking MQ to allocate a
		// unique message ID
		pmo.Options = syncpointSetting | ibmmq.MQPMO_NEW_MSG_ID

		// Convert the JMS persistence into the equivalent MQ message descriptor
		// attribute.
		if producer.deliveryMode == jms20subset.DeliveryMode_NON_PERSISTENT {
			putmqmd.Persistence = ibmmq.MQPER_NOT_PERSISTENT
		} else {
			putmqmd.Persistence = ibmmq.MQPER_PERSISTENT
		}

		var buffer []byte

		// We have a "Message" object and can use a switch to safely convert it
		// to the implementation type in order to extract generic MQ message
		switch typedMsg := msg.(type) {
		case *TextMessageImpl:

			// If the message already has an MQMD then use that (for example it might
			// contain ReplyTo information)
			if typedMsg.mqmd != nil {
				putmqmd = typedMsg.mqmd
			}

			// Store the Put MQMD so that we can later retrieve "out" fields like MsgId
			typedMsg.mqmd = putmqmd

			// Set up this MQ message to contain the string from the JMS message.
			putmqmd.Format = ibmmq.MQFMT_STRING
			msgStr := typedMsg.GetText()
			if msgStr != nil {
				buffer = []byte(*msgStr)
			}

		case *BytesMessageImpl:

			// If the message already has an MQMD then use that (for example it might
			// contain ReplyTo information)
			if typedMsg.mqmd != nil {
				putmqmd = typedMsg.mqmd
			}

			// Store the Put MQMD so that we can later retrieve "out" fields like MsgId
			typedMsg.mqmd = putmqmd

			// Set up this MQ message to contain the bytes from the JMS message.
			putmqmd.Format = ibmmq.MQFMT_NONE
			buffer = *typedMsg.ReadBytes()

		default:
			// This "should never happen"(!) apart from in situations where we are
			// part way through adding support for a new message type to this library.
			log.Fatal(jms20subset.CreateJMSException("UnexpectedMessageType", "UnexpectedMessageType-send1", nil))
		}

		// If the producer has a TTL specified then apply it to the put MQMD so
		// that MQ will honour it.
		if producer.timeToLive > 0 {
			// Note that JMS timeToLive in milliseconds, whereas MQMD Expiry expects
			// 10ths of a second
			putmqmd.Expiry = (int32(producer.timeToLive) / 100)
		}

		// Invoke the MQ command to put the message.
		// Any Err that occurs will be handled below.
		err = qObject.Put(putmqmd, pmo, buffer)

	}

	// Note that the following block handles errors for both opening the queue
	// and putting the message.
	if err != nil {

		rcInt := int(err.(*ibmmq.MQReturn).MQRC)
		errCode := strconv.Itoa(rcInt)
		reason := ibmmq.MQItoString("RC", rcInt)
		retErr = jms20subset.CreateJMSException(reason, errCode, err)

	}

	return retErr

}

// SetDeliveryMode contains the MQ logic necessary to store the specified
// delivery mode parameter inside the Producer object so that it can be
// applied when sending messages using this Producer.
func (producer *ProducerImpl) SetDeliveryMode(mode int) jms20subset.JMSProducer {

	// Check that the specified mode parameter is one of the values that we permit,
	// and if so store that value inside producer.
	if mode == jms20subset.DeliveryMode_PERSISTENT || mode == jms20subset.DeliveryMode_NON_PERSISTENT {
		producer.deliveryMode = mode

	} else {
		// Normally we would throw an error here to indicate that an invalid value
		// was specified, however we have decided that it is more useful to support
		// method chaining, which prevents us from returning an error object.
		// Instead we settle for printing an error message to the console.
		fmt.Println("Invalid DeliveryMode specified: " + strconv.Itoa(mode))
	}

	return producer
}

// GetDeliveryMode returns the current delivery mode that is set on this
// Producer.
func (producer *ProducerImpl) GetDeliveryMode() int {
	return producer.deliveryMode
}

// SetTimeToLive contains the MQ logic necessary to store the specified
// time to live parameter inside the Producer object so that it can be
// applied when sending messages using this Producer.
func (producer *ProducerImpl) SetTimeToLive(timeToLive int) jms20subset.JMSProducer {

	// Only accept a non-negative value for time to live.
	if timeToLive >= 0 {
		producer.timeToLive = timeToLive

	} else {
		// Normally we would throw an error here to indicate that an invalid value
		// was specified, however we have decided that it is more useful to support
		// method chaining, which prevents us from returning an error object.
		// Instead we settle for printing an error message to the console.
		fmt.Println("Invalid TimeToLive specified: " + strconv.FormatInt(int64(timeToLive), 10))
	}

	return producer
}

// GetTimeToLive returns the current time to live that is set on this
// Producer.
func (producer *ProducerImpl) GetTimeToLive() int {
	return producer.timeToLive
}
